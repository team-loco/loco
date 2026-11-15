// Package observabilityconfig resolves resource and deployment observability settings.
package observabilityconfig

import deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"

type Config struct {
	Logs                  bool
	Metrics               bool
	Traces                bool
	AutoinjectOtelEnvVars bool
}

func Defaults() Config {
	return Config{Logs: true, Metrics: true, Traces: true}
}

// Resolve applies each explicit deployment field over the resource settings and defaults.
func Resolve(resource, deployment *deploymentv1.ObservabilityConfig) Config {
	cfg := Defaults()
	for _, settings := range []*deploymentv1.ObservabilityConfig{resource, deployment} {
		if settings == nil {
			continue
		}
		if settings.Logs != nil {
			cfg.Logs = settings.GetLogs()
		}
		if settings.Metrics != nil {
			cfg.Metrics = settings.GetMetrics()
		}
		if settings.Traces != nil {
			cfg.Traces = settings.GetTraces()
		}
		if settings.AutoinjectOtelEnvVars != nil {
			cfg.AutoinjectOtelEnvVars = settings.GetAutoinjectOtelEnvVars()
		}
	}
	return cfg
}

func (c Config) Proto() *deploymentv1.ObservabilityConfig {
	return &deploymentv1.ObservabilityConfig{
		Logs: new(c.Logs), Metrics: new(c.Metrics), Traces: new(c.Traces),
		AutoinjectOtelEnvVars: new(c.AutoinjectOtelEnvVars),
	}
}
