package locofile

const (
	Version           = 1
	FileName          = "loco.yaml"
	DefaultDockerfile = "Dockerfile"
)

// File is the root of a loco.yaml file.
type File struct {
	Version  int                `json:"version"`
	Partial  string             `json:"partial"`
	Services map[string]Service `json:"services"`

	raw map[string]any
}

// Service is one deployable unit of the workspace.
type Service struct {
	Dockerfile   string              `json:"dockerfile,omitempty"`
	Context      string              `json:"context,omitempty"`
	Image        string              `json:"image,omitempty"`
	Port         *int32              `json:"port,omitempty"`
	Health       *Health             `json:"health,omitempty"`
	Routing      *Routing            `json:"routing,omitempty"`
	Domains      []string            `json:"domains,omitempty"`
	Env          map[string]string   `json:"env,omitempty"`
	Secrets      []string            `json:"secrets,omitempty"`
	Regions      map[string]Region   `json:"regions,omitempty"`
	Environments map[string]Override `json:"environments,omitempty"`
}

// Override is the part of a service that one environment changes.
type Override struct {
	Enabled    *bool             `json:"enabled,omitempty"`
	Dockerfile string            `json:"dockerfile,omitempty"`
	Context    string            `json:"context,omitempty"`
	Image      string            `json:"image,omitempty"`
	Port       *int32            `json:"port,omitempty"`
	Health     *Health           `json:"health,omitempty"`
	Routing    *Routing          `json:"routing,omitempty"`
	Domains    []string          `json:"domains,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Secrets    []string          `json:"secrets,omitempty"`
	Regions    map[string]Region `json:"regions,omitempty"`
}

// Health is the service health check.
type Health struct {
	Path               string `json:"path,omitempty"`
	Interval           *int32 `json:"interval,omitempty"`
	Timeout            *int32 `json:"timeout,omitempty"`
	FailThreshold      *int32 `json:"failThreshold,omitempty"`
	StartupGracePeriod *int32 `json:"startupGracePeriod,omitempty"`
}

// Routing is the service HTTP routing.
type Routing struct {
	PathPrefix  string `json:"pathPrefix,omitempty"`
	IdleTimeout *int32 `json:"idleTimeout,omitempty"`
}

// Region is the service placement in one region.
type Region struct {
	CPU         string       `json:"cpu,omitempty"`
	Memory      string       `json:"memory,omitempty"`
	Replicas    *Replicas    `json:"replicas,omitempty"`
	Autoscaling *Autoscaling `json:"autoscaling,omitempty"`
}

// Replicas bounds the replica count.
type Replicas struct {
	Min *int32 `json:"min,omitempty"`
	Max *int32 `json:"max,omitempty"`
}

// Autoscaling targets one metric. Exactly one of cpuTarget or memoryTarget is set.
type Autoscaling struct {
	CPUTarget    *int32 `json:"cpuTarget,omitempty"`
	MemoryTarget *int32 `json:"memoryTarget,omitempty"`
}
