package caddyprometheus

import (
	"fmt"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

func init() {
	httpcaddyfile.RegisterGlobalOption("prometheus", parseGlobalOption)
}

// parses the top-level "prometheus { ... }" block.
func parseGlobalOption(d *caddyfile.Dispenser, existingVal interface{}) (interface{}, error) {
	app := &App{}
	if existingVal != nil {
		app = existingVal.(*App)
	}

	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "config_file":
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				app.ConfigFile = d.Val()

			case "data_dir":
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				app.DataDir = d.Val()

			case "retention":
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return nil, d.Errf("parsing retention: %v", err)
				}
				app.Retention = caddy.Duration(dur)

			case "retention_size":
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				app.RetentionSize = d.Val()

			case "wal_compression":
				t := true
				app.WALCompression = &t

			case "listen":
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				app.ListenAddr = d.Val()

			case "enable_admin":
				app.EnableAdmin = true

			case "auto_scrape":
				t := true
				app.AutoScrape = &t

			case "query_timeout":
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return nil, d.Errf("parsing query_timeout: %v", err)
				}
				app.QueryTimeout = caddy.Duration(dur)

			case "query_max_samples":
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				var n int
				_, err := fmt.Sscanf(d.Val(), "%d", &n)
				if err != nil {
					return nil, d.Errf("parsing query_max_samples: %v", err)
				}
				app.QueryMaxSamples = n

			case "enable_remote_write_receiver":
				app.EnableRemoteWriteReceiver = true

			case "enable_otlp_write_receiver":
				app.EnableOTLPWriteReceiver = true

			case "out_of_order_time_window":
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				dur, err := caddy.ParseDuration(d.Val())
				if err != nil {
					return nil, d.Errf("parsing out_of_order_time_window: %v", err)
				}
				app.OutOfOrderTimeWindow = caddy.Duration(dur)

			case "global":
				g, err := parseGlobalBlock(d)
				if err != nil {
					return nil, err
				}
				app.Global = g

			case "scrape":
				sc, err := parseScrapeBlock(d)
				if err != nil {
					return nil, err
				}
				app.ScrapeConfigs = append(app.ScrapeConfigs, *sc)

			case "rule_files":
				app.RuleFiles = append(app.RuleFiles, d.RemainingArgs()...)

			case "alerting":
				ac, err := parseAlertingBlock(d)
				if err != nil {
					return nil, err
				}
				app.Alerting = ac

			case "remote_write":
				rw, err := parseRemoteWriteBlock(d)
				if err != nil {
					return nil, err
				}
				app.RemoteWrite = append(app.RemoteWrite, *rw)

			case "remote_read":
				rr, err := parseRemoteReadBlock(d)
				if err != nil {
					return nil, err
				}
				app.RemoteRead = append(app.RemoteRead, *rr)

			default:
				return nil, d.Errf("unrecognized option: %s", d.Val())
			}
		}
	}

	return httpcaddyfile.App{
		Name:  "prometheus",
		Value: caddyconfig.JSON(app, nil),
	}, nil
}

func parseGlobalBlock(d *caddyfile.Dispenser) (*GlobalConfig, error) {
	g := &GlobalConfig{}
	for d.NextBlock(1) {
		switch d.Val() {
		case "scrape_interval":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return nil, d.Errf("parsing scrape_interval: %v", err)
			}
			g.ScrapeInterval = caddy.Duration(dur)

		case "scrape_timeout":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return nil, d.Errf("parsing scrape_timeout: %v", err)
			}
			g.ScrapeTimeout = caddy.Duration(dur)

		case "evaluation_interval":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return nil, d.Errf("parsing evaluation_interval: %v", err)
			}
			g.EvaluationInterval = caddy.Duration(dur)

		case "external_labels":
			labels := make(map[string]string)
			for d.NextBlock(2) {
				key := d.Val()
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				labels[key] = d.Val()
			}
			g.ExternalLabels = labels

		default:
			return nil, d.Errf("unrecognized global option: %s", d.Val())
		}
	}
	return g, nil
}

func parseScrapeBlock(d *caddyfile.Dispenser) (*ScrapeConfig, error) {
	sc := &ScrapeConfig{}
	for d.NextBlock(1) {
		switch d.Val() {
		case "job_name":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			sc.JobName = d.Val()

		case "metrics_path":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			sc.MetricsPath = d.Val()

		case "scheme":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			sc.Scheme = d.Val()

		case "scrape_interval":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return nil, d.Errf("parsing scrape_interval: %v", err)
			}
			sc.ScrapeInterval = caddy.Duration(dur)

		case "scrape_timeout":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return nil, d.Errf("parsing scrape_timeout: %v", err)
			}
			sc.ScrapeTimeout = caddy.Duration(dur)

		case "static_configs":
			sg, err := parseStaticConfigs(d)
			if err != nil {
				return nil, err
			}
			sc.StaticConfigs = append(sc.StaticConfigs, *sg)

		case "file_sd_configs":
			fsd, err := parseFileSDConfigs(d)
			if err != nil {
				return nil, err
			}
			sc.FileSDConfigs = append(sc.FileSDConfigs, *fsd)

		case "relabel_configs":
			rc, err := parseRelabelConfig(d)
			if err != nil {
				return nil, err
			}
			sc.RelabelConfigs = append(sc.RelabelConfigs, *rc)

		case "metric_relabel_configs":
			rc, err := parseRelabelConfig(d)
			if err != nil {
				return nil, err
			}
			sc.MetricRelabelConfigs = append(sc.MetricRelabelConfigs, *rc)

		default:
			return nil, d.Errf("unrecognized scrape option: %s", d.Val())
		}
	}
	return sc, nil
}

func parseStaticConfigs(d *caddyfile.Dispenser) (*StaticConfig, error) {
	sg := &StaticConfig{}
	for d.NextBlock(2) {
		switch d.Val() {
		case "targets":
			sg.Targets = append(sg.Targets, d.RemainingArgs()...)
		case "labels":
			labels := make(map[string]string)
			for d.NextBlock(3) {
				key := d.Val()
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				labels[key] = d.Val()
			}
			sg.Labels = labels
		default:
			return nil, d.Errf("unrecognized static_configs option: %s", d.Val())
		}
	}
	return sg, nil
}

func parseFileSDConfigs(d *caddyfile.Dispenser) (*FileSDConfig, error) {
	fsd := &FileSDConfig{}
	for d.NextBlock(2) {
		switch d.Val() {
		case "files":
			fsd.Files = append(fsd.Files, d.RemainingArgs()...)
		case "refresh_interval":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return nil, d.Errf("parsing refresh_interval: %v", err)
			}
			fsd.RefreshInterval = caddy.Duration(dur)
		default:
			return nil, d.Errf("unrecognized file_sd_configs option: %s", d.Val())
		}
	}
	return fsd, nil
}

func parseRelabelConfig(d *caddyfile.Dispenser) (*RelabelConfig, error) {
	rc := &RelabelConfig{}
	for d.NextBlock(2) {
		switch d.Val() {
		case "source_labels":
			rc.SourceLabels = append(rc.SourceLabels, d.RemainingArgs()...)
		case "separator":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			rc.Separator = d.Val()
		case "target_label":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			rc.TargetLabel = d.Val()
		case "regex":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			rc.Regex = d.Val()
		case "replacement":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			rc.Replacement = d.Val()
		case "action":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			rc.Action = d.Val()
		default:
			return nil, d.Errf("unrecognized relabel option: %s", d.Val())
		}
	}
	return rc, nil
}

func parseAlertingBlock(d *caddyfile.Dispenser) (*AlertingConfig, error) {
	ac := &AlertingConfig{}
	for d.NextBlock(1) {
		switch d.Val() {
		case "alertmanagers":
			am := AlertmanagerConfig{}
			for d.NextBlock(2) {
				switch d.Val() {
				case "static_configs":
					sg, err := parseStaticConfigs(d)
					if err != nil {
						return nil, err
					}
					am.StaticConfigs = append(am.StaticConfigs, *sg)
				default:
					return nil, d.Errf("unrecognized alertmanagers option: %s", d.Val())
				}
			}
			ac.Alertmanagers = append(ac.Alertmanagers, am)
		default:
			return nil, d.Errf("unrecognized alerting option: %s", d.Val())
		}
	}
	return ac, nil
}

func parseRemoteWriteBlock(d *caddyfile.Dispenser) (*RemoteWriteConfig, error) {
	rw := &RemoteWriteConfig{}
	for d.NextBlock(1) {
		switch d.Val() {
		case "url":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			rw.URL = d.Val()
		case "basic_auth":
			ba, err := parseBasicAuth(d)
			if err != nil {
				return nil, err
			}
			rw.BasicAuth = ba
		default:
			return nil, d.Errf("unrecognized remote_write option: %s", d.Val())
		}
	}
	return rw, nil
}

func parseRemoteReadBlock(d *caddyfile.Dispenser) (*RemoteReadConfig, error) {
	rr := &RemoteReadConfig{}
	for d.NextBlock(1) {
		switch d.Val() {
		case "url":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			rr.URL = d.Val()
		case "basic_auth":
			ba, err := parseBasicAuth(d)
			if err != nil {
				return nil, err
			}
			rr.BasicAuth = ba
		default:
			return nil, d.Errf("unrecognized remote_read option: %s", d.Val())
		}
	}
	return rr, nil
}

func parseBasicAuth(d *caddyfile.Dispenser) (*BasicAuth, error) {
	ba := &BasicAuth{}
	for d.NextBlock(2) {
		switch d.Val() {
		case "username":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			ba.Username = d.Val()
		case "password":
			if !d.NextArg() {
				return nil, d.ArgErr()
			}
			ba.Password = d.Val()
		default:
			return nil, d.Errf("unrecognized basic_auth option: %s", d.Val())
		}
	}
	return ba, nil
}
