package caddyprometheus

import (
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"time"

	"github.com/caddyserver/caddy/v2"
	commonconfig "github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	promconfig "github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/discovery/file"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"go.yaml.in/yaml/v2"

	_ "github.com/prometheus/prometheus/discovery/install"
)

type AppConfig struct {
	ConfigFile                string         `json:"config_file,omitempty"` // path to prometheus.yml; mutually exclusive with inline config
	DataDir                   string         `json:"data_dir,omitempty"`    // default: {caddy_data}/prometheus
	Retention                 caddy.Duration `json:"retention,omitempty"`   // default: 15d
	RetentionSize             string         `json:"retention_size,omitempty"`
	WALCompression            *bool          `json:"wal_compression,omitempty"` // default: true
	ListenAddr                string         `json:"listen"`                    // required
	EnableAdmin               bool           `json:"enable_admin,omitempty"`
	AutoScrape                *bool          `json:"auto_scrape,omitempty"`          // default: true
	QueryTimeout              caddy.Duration `json:"query_timeout,omitempty"`        // default: 2m
	QueryMaxSamples           int            `json:"query_max_samples,omitempty"`    // default: 50000000
	QueryLookbackDelta        caddy.Duration `json:"query_lookback_delta,omitempty"` // default: 5m
	EnableRemoteWriteReceiver bool           `json:"enable_remote_write_receiver,omitempty"`
	EnableOTLPWriteReceiver   bool           `json:"enable_otlp_write_receiver,omitempty"`
	OutOfOrderTimeWindow      caddy.Duration `json:"out_of_order_time_window,omitempty"` // default: 0 (disabled)

	Global        *GlobalConfig       `json:"global,omitempty"`
	ScrapeConfigs []ScrapeConfig      `json:"scrape_configs,omitempty"`
	RuleFiles     []string            `json:"rule_files,omitempty"`
	Alerting      *AlertingConfig     `json:"alerting,omitempty"`
	RemoteWrite   []RemoteWriteConfig `json:"remote_write,omitempty"`
	RemoteRead    []RemoteReadConfig  `json:"remote_read,omitempty"`
}

type GlobalConfig struct {
	ScrapeInterval     caddy.Duration    `json:"scrape_interval,omitempty"`
	ScrapeTimeout      caddy.Duration    `json:"scrape_timeout,omitempty"`
	EvaluationInterval caddy.Duration    `json:"evaluation_interval,omitempty"`
	ExternalLabels     map[string]string `json:"external_labels,omitempty"`
}

type ScrapeConfig struct {
	JobName              string          `json:"job_name"`
	MetricsPath          string          `json:"metrics_path,omitempty"`
	Scheme               string          `json:"scheme,omitempty"`
	ScrapeInterval       caddy.Duration  `json:"scrape_interval,omitempty"`
	ScrapeTimeout        caddy.Duration  `json:"scrape_timeout,omitempty"`
	StaticConfigs        []StaticConfig  `json:"static_configs,omitempty"`
	FileSDConfigs        []FileSDConfig  `json:"file_sd_configs,omitempty"`
	RelabelConfigs       []RelabelConfig `json:"relabel_configs,omitempty"`
	MetricRelabelConfigs []RelabelConfig `json:"metric_relabel_configs,omitempty"`
}

type StaticConfig struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels,omitempty"`
}

type FileSDConfig struct {
	Files           []string       `json:"files"`
	RefreshInterval caddy.Duration `json:"refresh_interval,omitempty"`
}

type RelabelConfig struct {
	SourceLabels []string `json:"source_labels,omitempty"`
	Separator    string   `json:"separator,omitempty"`
	TargetLabel  string   `json:"target_label,omitempty"`
	Regex        string   `json:"regex,omitempty"`
	Modulus      uint64   `json:"modulus,omitempty"`
	Replacement  string   `json:"replacement,omitempty"`
	Action       string   `json:"action,omitempty"`
}

type AlertingConfig struct {
	Alertmanagers []AlertmanagerConfig `json:"alertmanagers"`
}

type AlertmanagerConfig struct {
	StaticConfigs []StaticConfig `json:"static_configs,omitempty"`
}

type RemoteWriteConfig struct {
	URL       string     `json:"url"`
	BasicAuth *BasicAuth `json:"basic_auth,omitempty"`
}

type RemoteReadConfig struct {
	URL       string     `json:"url"`
	BasicAuth *BasicAuth `json:"basic_auth,omitempty"`
}

type BasicAuth struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

func (c *AppConfig) defaults() {
	if c.DataDir == "" {
		c.DataDir = filepath.Join(caddy.AppDataDir(), "prometheus")
	}
	if c.Retention == 0 {
		c.Retention = caddy.Duration(15 * 24 * time.Hour)
	}
	if c.WALCompression == nil {
		t := true
		c.WALCompression = &t
	}
	if c.AutoScrape == nil {
		t := true
		c.AutoScrape = &t
	}
	if c.QueryTimeout == 0 {
		c.QueryTimeout = caddy.Duration(2 * time.Minute)
	}
	if c.QueryMaxSamples == 0 {
		c.QueryMaxSamples = 50_000_000
	}
	if c.QueryLookbackDelta == 0 {
		c.QueryLookbackDelta = caddy.Duration(5 * time.Minute)
	}
}

// loads from config_file or builds from inline (Caddyfile) config.
func (c *AppConfig) loadPrometheusConfig(logger *slog.Logger) (*promconfig.Config, error) {
	if c.ConfigFile != "" {
		return promconfig.LoadFile(c.ConfigFile, false, logger)
	}

	// Round-trip through YAML to set the internal 'loaded' flag.
	cfg, err := c.toPrometheusConfig()
	if err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshaling config: %w", err)
	}
	return promconfig.Load(string(out), logger)
}

func (c *AppConfig) toPrometheusConfig() (*promconfig.Config, error) {
	cfg := &promconfig.Config{GlobalConfig: promconfig.DefaultGlobalConfig}

	if c.Global != nil {
		if c.Global.ScrapeInterval != 0 {
			cfg.GlobalConfig.ScrapeInterval = model.Duration(c.Global.ScrapeInterval)
		}
		if c.Global.ScrapeTimeout != 0 {
			cfg.GlobalConfig.ScrapeTimeout = model.Duration(c.Global.ScrapeTimeout)
		}
		if c.Global.EvaluationInterval != 0 {
			cfg.GlobalConfig.EvaluationInterval = model.Duration(c.Global.EvaluationInterval)
		}
		if len(c.Global.ExternalLabels) > 0 {
			b := labels.NewBuilder(labels.EmptyLabels())
			for k, v := range c.Global.ExternalLabels {
				b.Set(k, v)
			}
			cfg.GlobalConfig.ExternalLabels = b.Labels()
		}
	}

	for i := range c.ScrapeConfigs {
		cfg.ScrapeConfigs = append(cfg.ScrapeConfigs,
			c.translateScrapeConfig(&c.ScrapeConfigs[i], &cfg.GlobalConfig))
	}
	cfg.RuleFiles = c.RuleFiles

	if c.Alerting != nil {
		for _, am := range c.Alerting.Alertmanagers {
			amCfg := &promconfig.AlertmanagerConfig{}
			for _, sc := range am.StaticConfigs {
				amCfg.ServiceDiscoveryConfigs = append(amCfg.ServiceDiscoveryConfigs,
					discovery.StaticConfig{{Targets: toTargets(sc.Targets), Labels: toLabelSet(sc.Labels)}})
			}
			cfg.AlertingConfig.AlertmanagerConfigs = append(cfg.AlertingConfig.AlertmanagerConfigs, amCfg)
		}
	}

	for _, rw := range c.RemoteWrite {
		rwCfg := &promconfig.RemoteWriteConfig{
			URL: &commonconfig.URL{URL: mustParseURL(rw.URL)},
		}
		if rw.BasicAuth != nil {
			rwCfg.HTTPClientConfig.BasicAuth = &commonconfig.BasicAuth{
				Username: rw.BasicAuth.Username,
				Password: commonconfig.Secret(rw.BasicAuth.Password),
			}
		}
		cfg.RemoteWriteConfigs = append(cfg.RemoteWriteConfigs, rwCfg)
	}

	for _, rr := range c.RemoteRead {
		rrCfg := &promconfig.RemoteReadConfig{
			URL: &commonconfig.URL{URL: mustParseURL(rr.URL)},
		}
		if rr.BasicAuth != nil {
			rrCfg.HTTPClientConfig.BasicAuth = &commonconfig.BasicAuth{
				Username: rr.BasicAuth.Username,
				Password: commonconfig.Secret(rr.BasicAuth.Password),
			}
		}
		cfg.RemoteReadConfigs = append(cfg.RemoteReadConfigs, rrCfg)
	}

	return cfg, nil
}

func (c *AppConfig) translateScrapeConfig(sc *ScrapeConfig, global *promconfig.GlobalConfig) *promconfig.ScrapeConfig {
	psc := &promconfig.ScrapeConfig{
		JobName:        sc.JobName,
		MetricsPath:    sc.MetricsPath,
		Scheme:         sc.Scheme,
		ScrapeInterval: model.Duration(sc.ScrapeInterval),
		ScrapeTimeout:  model.Duration(sc.ScrapeTimeout),
	}
	if psc.MetricsPath == "" {
		psc.MetricsPath = "/metrics"
	}
	if psc.Scheme == "" {
		psc.Scheme = "http"
	}
	if psc.ScrapeInterval == 0 {
		psc.ScrapeInterval = global.ScrapeInterval
	}
	if psc.ScrapeTimeout == 0 {
		psc.ScrapeTimeout = global.ScrapeTimeout
	}

	for _, sg := range sc.StaticConfigs {
		psc.ServiceDiscoveryConfigs = append(psc.ServiceDiscoveryConfigs,
			discovery.StaticConfig{{Targets: toTargets(sg.Targets), Labels: toLabelSet(sg.Labels)}})
	}
	for _, fsd := range sc.FileSDConfigs {
		psc.ServiceDiscoveryConfigs = append(psc.ServiceDiscoveryConfigs, &file.SDConfig{
			Files:           fsd.Files,
			RefreshInterval: model.Duration(fsd.RefreshInterval),
		})
	}
	for _, rc := range sc.RelabelConfigs {
		psc.RelabelConfigs = append(psc.RelabelConfigs, translateRelabelConfig(rc))
	}
	for _, rc := range sc.MetricRelabelConfigs {
		psc.MetricRelabelConfigs = append(psc.MetricRelabelConfigs, translateRelabelConfig(rc))
	}
	return psc
}

func translateRelabelConfig(rc RelabelConfig) *relabel.Config {
	c := &relabel.Config{
		Separator:   rc.Separator,
		TargetLabel: rc.TargetLabel,
		Replacement: rc.Replacement,
		Modulus:     rc.Modulus,
	}
	if c.Separator == "" {
		c.Separator = ";"
	}
	if c.Replacement == "" {
		c.Replacement = "$1"
	}
	for _, sl := range rc.SourceLabels {
		c.SourceLabels = append(c.SourceLabels, model.LabelName(sl))
	}
	if rc.Regex != "" {
		c.Regex = relabel.MustNewRegexp(rc.Regex)
	}
	if rc.Action != "" {
		c.Action = relabel.Action(rc.Action)
	} else {
		c.Action = relabel.Replace
	}
	return c
}

func toTargets(targets []string) []model.LabelSet {
	out := make([]model.LabelSet, len(targets))
	for i, t := range targets {
		out[i] = model.LabelSet{model.AddressLabel: model.LabelValue(t)}
	}
	return out
}

func toLabelSet(m map[string]string) model.LabelSet {
	if len(m) == 0 {
		return nil
	}
	ls := make(model.LabelSet, len(m))
	for k, v := range m {
		ls[model.LabelName(k)] = model.LabelValue(v)
	}
	return ls
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(fmt.Sprintf("invalid URL %q: %v", raw, err))
	}
	return u
}
