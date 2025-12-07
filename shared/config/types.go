package config

// AppConfig represents the full configuration from loco.toml
type AppConfig struct {
	Metadata  Metadata  `json:"metadata" toml:"Metadata"`
	Resources Resources `json:"resources" toml:"Resources"`
	Build     Build     `json:"build" toml:"Build"`
	Routing   Routing   `json:"routing" toml:"Routing"`
	Health    Health    `json:"health" toml:"Health"`
	Env       Env       `json:"env,omitzero" toml:"Env"`
	Obs       Obs       `json:"obs,omitzero" toml:"Obs"`
}

type Metadata struct {
	ConfigVersion string `json:"configVersion" toml:"ConfigVersion"`
	Description   string `json:"description,omitempty" toml:"Description"`
	Name          string `json:"name" toml:"Name"`
	Type          string `json:"type,omitempty" toml:"Type"`
}

type Resources struct {
	CPU      string   `json:"cpu" toml:"CPU"`
	Memory   string   `json:"memory" toml:"Memory"`
	Replicas Replicas `json:"replicas" toml:"Replicas"`
	Scalers  Scalers  `json:"scalers,omitzero" toml:"Scalers"`
}

type Replicas struct {
	Min int32 `json:"min" toml:"Min"`
	Max int32 `json:"max" toml:"Max"`
}

type Scalers struct {
	Enabled      bool  `json:"enabled" toml:"Enabled"`
	CPUTarget    int32 `json:"cpuTarget,omitempty" toml:"CPUTarget"`
	MemoryTarget int32 `json:"memoryTarget,omitempty" toml:"MemoryTarget"`
}

type Build struct {
	DockerfilePath string `json:"dockerfilePath" toml:"DockerfilePath"`
	Type           string `json:"type" toml:"Type"`
}

type Routing struct {
	Port        int32  `json:"port" toml:"Port"`
	Subdomain   string `json:"subdomain" toml:"Subdomain"`
	PathPrefix  string `json:"pathPrefix,omitempty" toml:"PathPrefix"`
	IdleTimeout int32  `json:"idleTimeout,omitempty" toml:"IdleTimeout"`
	Domain      string `json:"domain" toml:"Domain"`
}

type Health struct {
	Path               string `json:"path" toml:"Path"`
	Interval           int32  `json:"interval" toml:"Interval"`
	Timeout            int32  `json:"timeout" toml:"Timeout"`
	StartupGracePeriod int32  `json:"startupGracePeriod,omitempty" toml:"StartupGracePeriod"`
	FailThreshold      int32  `json:"failThreshold,omitempty" toml:"FailThreshold"`
}

type Env struct {
	File      string            `json:"file,omitempty" toml:"File"`
	Variables map[string]string `json:"variables,omitempty" toml:"Variables"`
}

type Obs struct {
	Logging Logging `json:"logging,omitzero" toml:"Logging"`
	Metrics Metrics `json:"metrics,omitzero" toml:"Metrics"`
	Tracing Tracing `json:"tracing,omitzero" toml:"Tracing"`
}

type Logging struct {
	Enabled         bool   `json:"enabled" toml:"Enabled"`
	RetentionPeriod string `json:"retentionPeriod,omitempty" toml:"RetentionPeriod"`
	Structured      bool   `json:"structured,omitempty" toml:"Structured"`
}

type Metrics struct {
	Enabled bool   `json:"enabled" toml:"Enabled"`
	Path    string `json:"path,omitempty" toml:"Path"`
	Port    int32  `json:"port,omitempty" toml:"Port"`
}

type Tracing struct {
	Enabled    bool              `json:"enabled" toml:"Enabled"`
	SampleRate float64           `json:"sampleRate,omitempty" toml:"SampleRate"`
	Tags       map[string]string `json:"tags,omitempty" toml:"Tags"`
}
