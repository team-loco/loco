package configplan

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/internal/locofile"
)

const (
	pathImage      = "image"
	pathDockerfile = "dockerfile"
	pathContext    = "context"

	defaultDockerfile = "Dockerfile"
	defaultContext    = "."
	pathPort          = "port"
	pathHealth        = "health"
	pathRouting       = "routing"
	pathDomains       = "domains"
	pathEnv           = "env"
	pathSecrets       = "secrets"
	pathRegions       = "regions"
	pathEnabled       = "enabled"

	listSeparator = ", "
)

// enabledChange and disabledChange are the one change of a service the environment starts or
// stops running; the planner emits them outside the field diff.
var (
	enabledChange  = Change{Path: pathEnabled, Before: "false", After: "true"}
	disabledChange = Change{Path: pathEnabled, Before: "true", After: "false"}
)

// State is a service as the planner compares it: the file service with the API defaults
// filled in, or the live service as the environment runs it. Image is the digest-pinned
// reference of an image service and empty for a service built from source; Dockerfile and
// Context are set for a service built from source and empty for an image service.
type State struct {
	Image      string
	Dockerfile string
	Context    string
	Port       int32
	Health     Health
	Routing    *Routing
	Domains    []string
	Env        map[string]string
	Secrets    []string
	Regions    map[string]Region
}

// Health is a service health check with every field set.
type Health struct {
	Path               string
	Interval           int32
	Timeout            int32
	FailThreshold      int32
	StartupGracePeriod int32
}

// Routing is the HTTP routing of a service with every field set.
type Routing struct {
	PathPrefix  string
	IdleTimeout int32
}

// Region is the placement of a service in one region.
type Region struct {
	CPU         string
	Memory      string
	MinReplicas int32
	MaxReplicas int32
	Autoscaling *Autoscaling
}

// Autoscaling holds the one target autoscaling follows; the other is zero.
type Autoscaling struct {
	CPUTarget    int32
	MemoryTarget int32
}

func fileState(service locofile.Service, pinnedImage string, defaults servicedefaults.Defaults) State {
	state := State{
		Image:   pinnedImage,
		Port:    defaults.Port,
		Health:  DefaultHealth(defaults),
		Domains: slices.Clone(service.Domains),
		Env:     maps.Clone(service.Env),
		Secrets: slices.Sorted(slices.Values(service.Secrets)),
		Regions: make(map[string]Region, len(service.Regions)),
	}
	if pinnedImage == "" {
		state.Dockerfile = defaultDockerfile
		state.Context = defaultContext
		if service.Dockerfile != "" {
			state.Dockerfile = service.Dockerfile
		}
		if service.Context != "" {
			state.Context = service.Context
		}
	}
	if service.Port != nil {
		state.Port = *service.Port
	}
	if service.Health != nil {
		state.Health = fileHealth(*service.Health, defaults)
	}
	if service.Routing != nil {
		routing := Routing{PathPrefix: service.Routing.PathPrefix, IdleTimeout: defaults.IdleTimeout}
		if routing.PathPrefix == "" {
			routing.PathPrefix = defaults.PathPrefix
		}
		if service.Routing.IdleTimeout != nil {
			routing.IdleTimeout = *service.Routing.IdleTimeout
		}
		state.Routing = &routing
	}
	for name, region := range service.Regions {
		state.Regions[name] = fileRegion(region)
	}
	return state
}

// DefaultHealth is the health check the API uses when a service sets none.
func DefaultHealth(defaults servicedefaults.Defaults) Health {
	return Health{
		Path:               defaults.HealthPath,
		Interval:           defaults.HealthInterval,
		Timeout:            defaults.HealthTimeout,
		FailThreshold:      defaults.HealthFailThreshold,
		StartupGracePeriod: defaults.HealthStartupGracePeriod,
	}
}

func fileHealth(health locofile.Health, defaults servicedefaults.Defaults) Health {
	filled := DefaultHealth(defaults)
	if health.Path != "" {
		filled.Path = health.Path
	}
	if health.Interval != nil {
		filled.Interval = *health.Interval
	}
	if health.Timeout != nil {
		filled.Timeout = *health.Timeout
	}
	if health.FailThreshold != nil {
		filled.FailThreshold = *health.FailThreshold
	}
	if health.StartupGracePeriod != nil {
		filled.StartupGracePeriod = *health.StartupGracePeriod
	}
	return filled
}

func fileRegion(region locofile.Region) Region {
	filled := Region{CPU: region.CPU, Memory: region.Memory}
	if region.Replicas != nil {
		if region.Replicas.Min != nil {
			filled.MinReplicas = *region.Replicas.Min
		}
		if region.Replicas.Max != nil {
			filled.MaxReplicas = *region.Replicas.Max
		}
	}
	if region.Autoscaling != nil {
		autoscaling := Autoscaling{}
		if region.Autoscaling.CPUTarget != nil {
			autoscaling.CPUTarget = *region.Autoscaling.CPUTarget
		}
		if region.Autoscaling.MemoryTarget != nil {
			autoscaling.MemoryTarget = *region.Autoscaling.MemoryTarget
		}
		filled.Autoscaling = &autoscaling
	}
	return filled
}

func flatten(state *State) map[string]string {
	if state == nil {
		return map[string]string{}
	}
	fields := map[string]string{
		pathPort:                           formatInt(state.Port),
		pathHealth + ".path":               state.Health.Path,
		pathHealth + ".interval":           formatInt(state.Health.Interval),
		pathHealth + ".timeout":            formatInt(state.Health.Timeout),
		pathHealth + ".failThreshold":      formatInt(state.Health.FailThreshold),
		pathHealth + ".startupGracePeriod": formatInt(state.Health.StartupGracePeriod),
		pathDomains:                        formatList(state.Domains),
		pathSecrets:                        formatList(slices.Sorted(slices.Values(state.Secrets))),
	}
	if state.Image != "" {
		fields[pathImage] = state.Image
	}
	if state.Dockerfile != "" {
		fields[pathDockerfile] = state.Dockerfile
	}
	if state.Context != "" {
		fields[pathContext] = state.Context
	}
	if state.Routing != nil {
		fields[pathRouting+".pathPrefix"] = state.Routing.PathPrefix
		fields[pathRouting+".idleTimeout"] = formatInt(state.Routing.IdleTimeout)
	}
	for key, value := range state.Env {
		fields[pathEnv+"."+key] = value
	}
	for name, region := range state.Regions {
		prefix := pathRegions + "." + name + "."
		fields[prefix+"cpu"] = region.CPU
		fields[prefix+"memory"] = region.Memory
		fields[prefix+"replicas.min"] = formatInt(region.MinReplicas)
		fields[prefix+"replicas.max"] = formatInt(region.MaxReplicas)
		if region.Autoscaling == nil {
			continue
		}
		if region.Autoscaling.CPUTarget != 0 {
			fields[prefix+"autoscaling.cpuTarget"] = formatInt(region.Autoscaling.CPUTarget)
		}
		if region.Autoscaling.MemoryTarget != 0 {
			fields[prefix+"autoscaling.memoryTarget"] = formatInt(region.Autoscaling.MemoryTarget)
		}
	}
	return fields
}

func diff(before, after *State) []Change {
	beforeFields := flatten(before)
	afterFields := flatten(after)
	paths := make(map[string]struct{}, len(beforeFields)+len(afterFields))
	for path := range beforeFields {
		paths[path] = struct{}{}
	}
	for path := range afterFields {
		paths[path] = struct{}{}
	}
	var changes []Change
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		if beforeFields[path] == afterFields[path] {
			continue
		}
		changes = append(changes, Change{Path: path, Before: beforeFields[path], After: afterFields[path]})
	}
	return changes
}

func formatInt(value int32) string {
	return strconv.FormatInt(int64(value), 10)
}

func formatList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return "[" + strings.Join(values, listSeparator) + "]"
}
