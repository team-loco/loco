package loco

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	namePattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	dnsPattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	envPattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	quantityPattern = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)(?:[numkMGTPE]|[KMGTPE]i)?$`)
)

func Normalize(manifest *Manifest) error {
	if manifest == nil || manifest.Version != ProtocolVersion {
		return errors.New("unsupported infrastructure protocol version")
	}
	if !namePattern.MatchString(manifest.Stack.Name) {
		return errors.New(
			"stack name must start with a lowercase letter and contain at most 63 letters, digits or hyphens",
		)
	}
	keys := make(map[string]bool, len(manifest.Stack.Services))
	names := make(map[string]bool, len(manifest.Stack.Services))
	for i := range manifest.Stack.Services {
		service := &manifest.Stack.Services[i]
		if err := normalizeService(service); err != nil {
			return fmt.Errorf("service %q: %w", service.Key, err)
		}
		if keys[service.Key] || names[service.Name] {
			return fmt.Errorf("duplicate service key or name %q", service.Key)
		}
		keys[service.Key] = true
		names[service.Name] = true
	}
	slices.SortFunc(manifest.Stack.Services, func(a, b Service) int { return strings.Compare(a.Key, b.Key) })
	return nil
}

func normalizeService(service *Service) error {
	if service.Name == "" {
		service.Name = service.Key
	}
	if !namePattern.MatchString(service.Key) || !namePattern.MatchString(service.Name) {
		return errors.New("key and name must be lowercase names of at most 63 characters")
	}
	if len(service.Description) > 256 {
		return errors.New("description exceeds 256 characters")
	}
	if (service.Build == nil) == (service.Image == "") {
		return errors.New("provide exactly one build or image source")
	}
	if service.Build != nil {
		if service.Build.Context == "" {
			service.Build.Context = "."
		}
		if service.Build.Dockerfile == "" {
			service.Build.Dockerfile = "Dockerfile"
		}
	}
	if err := normalizeRouting(service); err != nil {
		return err
	}
	if err := validateRegions(service); err != nil {
		return err
	}
	if err := normalizeHealth(&service.Health); err != nil {
		return err
	}
	if err := validateVariables(service.Env); err != nil {
		return err
	}
	return normalizeObservability(&service.Observability)
}

func normalizeRouting(service *Service) error {
	if service.Routing.Port < 1024 || service.Routing.Port > 65535 {
		return errors.New("routing port must be between 1024 and 65535")
	}
	if service.Routing.PathPrefix == "" {
		service.Routing.PathPrefix = "/"
	}
	if !strings.HasPrefix(service.Routing.PathPrefix, "/") {
		return errors.New("routing pathPrefix must start with /")
	}
	if service.Routing.IdleTimeout == nil {
		service.Routing.IdleTimeout = Value(int32(60))
	}
	if *service.Routing.IdleTimeout < 0 {
		return errors.New("routing idleTimeout cannot be negative")
	}
	if service.Domain != nil {
		host := strings.ToLower(service.Domain.Hostname)
		if len(host) > 253 || len(strings.Split(host, ".")) < 2 {
			return errors.New("domain requires a full hostname")
		}
		for _, label := range strings.Split(host, ".") {
			if !dnsPattern.MatchString(label) {
				return errors.New("domain contains an invalid DNS label")
			}
		}
		service.Domain.Hostname = host
	}
	return nil
}

func validateRegions(service *Service) error {
	if len(service.Regions) == 0 || service.PrimaryRegion == "" {
		return errors.New("regions and primaryRegion are required")
	}
	if _, ok := service.Regions[service.PrimaryRegion]; !ok {
		return errors.New("primaryRegion must name a configured region")
	}
	for name, region := range service.Regions {
		if !namePattern.MatchString(name) {
			return fmt.Errorf("invalid region name %q", name)
		}
		if !positiveQuantity(region.CPU) || !positiveQuantity(region.Memory) {
			return fmt.Errorf("region %q requires positive CPU and memory quantities", name)
		}
		if region.ReplicasMin < 1 || region.ReplicasMax < region.ReplicasMin {
			return fmt.Errorf("region %q requires 1 <= replicasMin <= replicasMax", name)
		}
		if region.Autoscaling != nil {
			if err := validateAutoscaling(region.Autoscaling); err != nil {
				return fmt.Errorf("region %q: %w", name, err)
			}
		}
	}
	return nil
}

func positiveQuantity(value string) bool {
	return quantityPattern.MatchString(value) && strings.ContainsAny(value, "123456789")
}

func validateAutoscaling(scaler *Autoscaling) error {
	if (scaler.CPUTarget == nil) == (scaler.MemoryTarget == nil) {
		return errors.New("autoscaling requires exactly one cpuTarget or memoryTarget")
	}
	for _, target := range []*int32{scaler.CPUTarget, scaler.MemoryTarget} {
		if target != nil && (*target < 1 || *target > 100) {
			return errors.New("autoscaling target must be between 1 and 100")
		}
	}
	return nil
}

func normalizeHealth(health *Health) error {
	if !strings.HasPrefix(health.Path, "/") {
		return errors.New("health path must start with /")
	}
	if health.Interval == 0 {
		health.Interval = 30
	}
	if health.Timeout == 0 {
		health.Timeout = 5
	}
	if health.FailThreshold == 0 {
		health.FailThreshold = 3
	}
	if health.Interval < 5 || health.Timeout < 1 || health.Timeout > 60 || health.FailThreshold < 1 ||
		health.FailThreshold > 10 ||
		health.StartupGracePeriod < 0 ||
		health.StartupGracePeriod > 180 {
		return errors.New("health requires interval >= 5, timeout 1-60, failThreshold 1-10 and startup grace 0-180")
	}
	return nil
}

func validateVariables(variables map[string]Variable) error {
	for name, variable := range variables {
		if !envPattern.MatchString(name) {
			return fmt.Errorf("invalid variable name %q", name)
		}
		switch variable.Kind {
		case VariableLiteral:
			if !utf8.ValidString(variable.Value) || strings.ContainsRune(variable.Value, 0) ||
				len(variable.Value) > 64<<10 {
				return fmt.Errorf("variable %q must contain UTF-8 text without NUL, at most 64 KiB", name)
			}
			if variable.Name != "" {
				return fmt.Errorf("literal variable %q cannot name a secret", name)
			}
		case VariableSecret:
			if !namePattern.MatchString(variable.Name) || variable.Value != "" {
				return fmt.Errorf("secret variable %q requires a secret name and no literal value", name)
			}
		case VariablePreserve:
			if variable.Name != "" || variable.Value != "" {
				return fmt.Errorf("preserved variable %q cannot provide a value", name)
			}
		default:
			return fmt.Errorf("variable %q has an unsupported kind", name)
		}
	}
	return nil
}

func normalizeObservability(obs *Observability) error {
	if obs.Logging.Enabled == nil {
		obs.Logging.Enabled = Value(true)
	}
	if obs.Logging.RetentionPeriod == "" {
		obs.Logging.RetentionPeriod = "7d"
	}
	retention := strings.TrimSuffix(obs.Logging.RetentionPeriod, "d")
	if retention != obs.Logging.RetentionPeriod {
		retention += "h"
	}
	if duration, err := time.ParseDuration(retention); err != nil || duration <= 0 {
		return errors.New("logging retention must be a positive duration")
	}
	if obs.Metrics.Path == "" {
		obs.Metrics.Path = "/metrics"
	}
	if obs.Metrics.Port == 0 {
		obs.Metrics.Port = 9090
	}
	if obs.Metrics.Enabled && (!strings.HasPrefix(obs.Metrics.Path, "/") ||
		obs.Metrics.Port < 1 || obs.Metrics.Port > 65535) {
		return errors.New("metrics requires a valid path and port")
	}
	if obs.Tracing.SampleRate == nil {
		obs.Tracing.SampleRate = Value(0.1)
	}
	if rate := *obs.Tracing.SampleRate; math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
		return errors.New("tracing sampleRate must be between 0 and 1")
	}
	return nil
}
