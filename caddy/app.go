// Package caddyprometheus embeds a full Prometheus server inside Caddy.
//
// Imports Prometheus's own Go packages (TSDB, PromQL, scrape, rules,
// discovery, web) and wires them into Caddy's module lifecycle.
package caddyprometheus

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	promconfig "github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/notifier"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/rules"
	"github.com/prometheus/prometheus/scrape"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/storage/remote"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/util/compression"
	"github.com/prometheus/prometheus/util/notifications"
	"github.com/prometheus/prometheus/web"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"
)

func init() { caddy.RegisterModule(new(App)) }

type App struct {
	AppConfig

	db                 *tsdb.DB
	fanoutStorage      storage.Storage
	remoteStorage      *remote.Storage
	queryEngine        *promql.Engine
	scrapeManager      *scrape.Manager
	ruleManager        *rules.Manager
	notifierMgr        *notifier.Manager
	discoveryMgrScrape *discovery.Manager
	discoveryMgrNotify *discovery.Manager
	webHandler         *web.Handler
	webListener        net.Listener
	webAddr            string

	logger          *zap.Logger
	slogger         *slog.Logger
	registry        *prometheus.Registry
	httpListenAddrs []string
	cancelScrape    context.CancelFunc
	cancelNotify    context.CancelFunc
	cancelWeb       context.CancelFunc
}

func (*App) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "prometheus",
		New: func() caddy.Module { return new(App) },
	}
}

func (a *App) Provision(ctx caddy.Context) error {
	a.logger = ctx.Logger()
	a.slogger = slog.New(zapslog.NewHandler(a.logger.Core(), nil))
	a.registry = prometheus.NewRegistry()
	a.defaults()
	a.httpListenAddrs = discoverHTTPAddrs(ctx)
	return nil
}

func (a *App) Start() error {
	if a.ListenAddr == "" {
		return fmt.Errorf("prometheus: listen address is required (e.g. listen :9090)")
	}

	cfg, err := a.loadPrometheusConfig(a.slogger)
	if err != nil {
		return fmt.Errorf("prometheus config: %w", err)
	}

	tsdbOpts := tsdb.DefaultOptions()
	tsdbOpts.RetentionDuration = int64(time.Duration(a.Retention) / time.Millisecond)
	if a.WALCompression != nil && *a.WALCompression {
		tsdbOpts.WALCompression = compression.Snappy
	}
	dbStats := tsdb.NewDBStats()
	a.db, err = tsdb.Open(a.DataDir, a.slogger, a.registry, tsdbOpts, dbStats)
	if err != nil {
		return fmt.Errorf("tsdb open: %w", err)
	}

	startTimeFunc := func() (int64, error) {
		if blocks := a.db.Blocks(); len(blocks) > 0 {
			return blocks[0].Meta().MinTime, nil
		}
		return int64(model.Latest), nil
	}
	a.remoteStorage = remote.NewStorage(a.slogger, a.registry, startTimeFunc, a.DataDir+"/wal", time.Minute, nil, false)
	a.fanoutStorage = storage.NewFanout(a.slogger, a.db, a.remoteStorage)

	a.queryEngine = promql.NewEngine(promql.EngineOpts{
		Logger:               a.slogger,
		Reg:                  a.registry,
		MaxSamples:           a.QueryMaxSamples,
		Timeout:              time.Duration(a.QueryTimeout),
		LookbackDelta:        time.Duration(a.QueryLookbackDelta),
		EnableAtModifier:     true,
		EnableNegativeOffset: true,
	})

	a.notifierMgr = notifier.NewManager(
		&notifier.Options{Registerer: a.registry},
		model.UTF8Validation,
		a.slogger,
	)

	sdMetrics, err := discovery.CreateAndRegisterSDMetrics(a.registry)
	if err != nil {
		return fmt.Errorf("sd metrics: %w", err)
	}
	ctxScrape, cancelScrape := context.WithCancel(context.Background())
	a.cancelScrape = cancelScrape
	a.discoveryMgrScrape = discovery.NewManager(ctxScrape, a.slogger, a.registry, sdMetrics, discovery.Name("scrape"))

	ctxNotify, cancelNotify := context.WithCancel(context.Background())
	a.cancelNotify = cancelNotify
	a.discoveryMgrNotify = discovery.NewManager(ctxNotify, a.slogger, a.registry, sdMetrics, discovery.Name("notify"))

	a.scrapeManager, err = scrape.NewManager(
		&scrape.Options{},
		a.slogger,
		nil, // no JSON failure logger
		nil, // unused in v2 path
		a.fanoutStorage,
		a.registry,
	)
	if err != nil {
		return fmt.Errorf("scrape manager: %w", err)
	}

	externalURL, _ := url.Parse("http://localhost")
	a.ruleManager = rules.NewManager(&rules.ManagerOptions{
		ExternalURL: externalURL,
		QueryFunc:   rules.EngineQueryFunc(a.queryEngine, a.fanoutStorage),
		NotifyFunc:  rules.SendAlerts(a.notifierMgr, externalURL.String()),
		Context:     context.Background(),
		Appendable:  a.fanoutStorage,
		Queryable:   a.fanoutStorage,
		Logger:      a.slogger,
		Registerer:  a.registry,
	})

	// serves web ui
	ctxWeb, cancelWeb := context.WithCancel(context.Background())
	a.cancelWeb = cancelWeb

	a.webListener, err = net.Listen("tcp", a.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", a.ListenAddr, err)
	}
	a.webAddr = a.webListener.Addr().String()

	a.webHandler = web.New(a.slogger, &web.Options{
		Context:                   ctxWeb,
		Storage:                   a.fanoutStorage,
		LocalStorage:              &localStorageAdapter{DB: a.db, dbStats: dbStats},
		QueryEngine:               a.queryEngine,
		ScrapeManager:             a.scrapeManager,
		RuleManager:               a.ruleManager,
		Notifier:                  a.notifierMgr,
		RoutePrefix:               "/",
		ExternalURL:               externalURL,
		Flags:                     map[string]string{"storage.tsdb.path": a.DataDir},
		TSDBDir:                   a.DataDir,
		TSDBRetentionDuration:     model.Duration(a.Retention),
		EnableAdminAPI:            a.EnableAdmin,
		EnableRemoteWriteReceiver: a.EnableRemoteWriteReceiver,
		ListenAddresses:           []string{a.webAddr},
		Gatherer:                  a.registry,
		Registerer:                a.registry,
		LookbackDelta:             time.Duration(a.QueryLookbackDelta),
		NotificationsGetter:       func() []notifications.Notification { return nil },
		NotificationsSub:          func() (<-chan notifications.Notification, func(), bool) { return nil, func() {}, false },
	})
	a.webHandler.SetReady(web.Ready)

	// Auto-scrape injects targets for Caddy's admin metrics and HTTP server addresses.
	if a.AutoScrape != nil && *a.AutoScrape {
		a.injectAutoScrapeConfigs(cfg)
	}

	// Distribute config to all subsystems and start them.
	if err := a.applyConfig(cfg); err != nil {
		return fmt.Errorf("apply config: %w", err)
	}
	go func() {
		if err := a.discoveryMgrScrape.Run(); err != nil {
			a.slogger.Error("scrape discovery failed", "err", err)
		}
	}()
	go func() {
		if err := a.discoveryMgrNotify.Run(); err != nil {
			a.slogger.Error("notify discovery failed", "err", err)
		}
	}()
	go func() {
		if err := a.scrapeManager.Run(a.discoveryMgrScrape.SyncCh()); err != nil {
			a.slogger.Error("scrape manager failed", "err", err)
		}
	}()
	go a.ruleManager.Run() // blocks until Stop()
	go a.notifierMgr.Run(a.discoveryMgrNotify.SyncCh())
	go func() {
		if err := a.webHandler.Run(ctxWeb, []net.Listener{a.webListener}, ""); err != nil {
			a.slogger.Error("web handler failed", "err", err)
		}
	}()

	a.slogger.Info("prometheus started", "data_dir", a.DataDir, "listen", a.webAddr)
	return nil
}

func (a *App) Stop() error {
	a.slogger.Info("prometheus stopping")
	if a.cancelScrape != nil {
		a.cancelScrape()
	}
	if a.cancelNotify != nil {
		a.cancelNotify()
	}
	if a.ruleManager != nil {
		a.ruleManager.Stop()
	}
	if a.scrapeManager != nil {
		a.scrapeManager.Stop()
	}
	if a.cancelWeb != nil {
		a.cancelWeb()
	}
	if a.notifierMgr != nil {
		a.notifierMgr.Stop()
	}
	if a.fanoutStorage != nil {
		if err := a.fanoutStorage.Close(); err != nil {
			a.slogger.Error("closing storage", "err", err)
		}
	}
	return nil
}

// injectAutoScrapeConfigs adds scrape targets for Caddy's admin endpoint
// and any discovered HTTP server addresses.
func (a *App) injectAutoScrapeConfigs(cfg *promconfig.Config) {
	inherit := func(job string, targets []model.LabelSet) *promconfig.ScrapeConfig {
		return &promconfig.ScrapeConfig{
			JobName:                    job,
			MetricsPath:                "/metrics",
			Scheme:                     "http",
			ScrapeInterval:             cfg.GlobalConfig.ScrapeInterval,
			ScrapeTimeout:              cfg.GlobalConfig.ScrapeTimeout,
			MetricNameValidationScheme: cfg.GlobalConfig.MetricNameValidationScheme,
			MetricNameEscapingScheme:   cfg.GlobalConfig.MetricNameEscapingScheme,
			ServiceDiscoveryConfigs:    discovery.Configs{discovery.StaticConfig{{Targets: targets}}},
		}
	}

	cfg.ScrapeConfigs = append(cfg.ScrapeConfigs,
		inherit("caddy_admin", []model.LabelSet{{model.AddressLabel: "localhost:2019"}}),
	)

	if len(a.httpListenAddrs) > 0 {
		targets := make([]model.LabelSet, len(a.httpListenAddrs))
		for i, addr := range a.httpListenAddrs {
			targets[i] = model.LabelSet{model.AddressLabel: model.LabelValue(addr)}
		}
		cfg.ScrapeConfigs = append(cfg.ScrapeConfigs, inherit("caddy_sites", targets))
	}
}

func (a *App) applyConfig(cfg *promconfig.Config) error {
	scrapeDiscovery := make(map[string]discovery.Configs)
	for _, sc := range cfg.ScrapeConfigs {
		scrapeDiscovery[sc.JobName] = sc.ServiceDiscoveryConfigs
	}
	if err := a.discoveryMgrScrape.ApplyConfig(scrapeDiscovery); err != nil {
		return fmt.Errorf("scrape discovery: %w", err)
	}

	notifyDiscovery := make(map[string]discovery.Configs)
	for i, am := range cfg.AlertingConfig.AlertmanagerConfigs {
		notifyDiscovery[fmt.Sprintf("notify_%d", i)] = am.ServiceDiscoveryConfigs
	}
	if err := a.discoveryMgrNotify.ApplyConfig(notifyDiscovery); err != nil {
		return fmt.Errorf("notify discovery: %w", err)
	}

	if err := a.scrapeManager.ApplyConfig(cfg); err != nil {
		return fmt.Errorf("scrape manager: %w", err)
	}

	if len(cfg.RuleFiles) > 0 {
		var files []string
		for _, pat := range cfg.RuleFiles {
			matches, err := filepath.Glob(pat)
			if err != nil {
				return fmt.Errorf("rule glob %q: %w", pat, err)
			}
			files = append(files, matches...)
		}
		if err := a.ruleManager.Update(
			time.Duration(cfg.GlobalConfig.EvaluationInterval),
			files, cfg.GlobalConfig.ExternalLabels, "", nil,
		); err != nil {
			return fmt.Errorf("rule manager: %w", err)
		}
	}

	if err := a.notifierMgr.ApplyConfig(cfg); err != nil {
		return fmt.Errorf("notifier: %w", err)
	}
	if err := a.remoteStorage.ApplyConfig(cfg); err != nil {
		return fmt.Errorf("remote storage: %w", err)
	}
	return nil
}

// localStorageAdapter bridges tsdb.DB to the web.LocalStorage interface.
// The web package expects ([]BlockMeta, error) but tsdb.DB returns []BlockMeta.
type localStorageAdapter struct {
	*tsdb.DB
	dbStats *tsdb.DBStats
}

func (l *localStorageAdapter) BlockMetas() ([]tsdb.BlockMeta, error) {
	return l.DB.BlockMetas(), nil
}

func (l *localStorageAdapter) Stats(statsByLabelName string, limit int) (*tsdb.Stats, error) {
	return l.DB.Head().Stats(statsByLabelName, limit), nil
}

func (l *localStorageAdapter) WALReplayStatus() (tsdb.WALReplayStatus, error) {
	if l.dbStats != nil && l.dbStats.Head != nil && l.dbStats.Head.WALReplayStatus != nil {
		return l.dbStats.Head.WALReplayStatus.GetWALReplayStatus(), nil
	}
	return tsdb.WALReplayStatus{}, nil
}

// discoverHTTPAddrs finds all listen addresses from Caddy's HTTP servers.
// Used by auto_scrape to create targets for per-site metrics.
func discoverHTTPAddrs(ctx caddy.Context) []string {
	httpAppIface, err := ctx.App("http")
	if err != nil {
		return nil
	}
	httpApp, ok := httpAppIface.(*caddyhttp.App)
	if !ok || httpApp == nil {
		return nil
	}
	var addrs []string
	for _, srv := range httpApp.Servers {
		for _, ln := range srv.Listen {
			addr, err := caddy.ParseNetworkAddress(ln)
			if err != nil {
				continue
			}
			// Expand port ranges into individual targets.
			for p := addr.StartPort; p <= addr.EndPort; p++ {
				host := addr.Host
				if host == "" {
					host = "localhost"
				}
				addrs = append(addrs, net.JoinHostPort(host, fmt.Sprintf("%d", p)))
			}
		}
	}
	return addrs
}

var (
	_ caddy.App         = (*App)(nil)
	_ caddy.Provisioner = (*App)(nil)
)
