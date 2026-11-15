// Package observabilityconfig resolves resource and deployment observability settings.
package observabilityconfig

import deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"

type Config struct {
	InjectOtel bool
}

func Defaults() Config {
	return Config{InjectOtel: true}
}

// Resolve applies an explicit deployment value over the resource setting and default.
func Resolve(resource, deployment *deploymentv1.ObservabilityConfig) Config {
	cfg := Defaults()
	for _, settings := range []*deploymentv1.ObservabilityConfig{resource, deployment} {
		if settings != nil && settings.InjectOtel != nil {
			cfg.InjectOtel = settings.GetInjectOtel()
		}
	}
	return cfg
}

func (c Config) Proto() *deploymentv1.ObservabilityConfig {
	return &deploymentv1.ObservabilityConfig{InjectOtel: new(c.InjectOtel)}
}
