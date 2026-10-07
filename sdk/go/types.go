package loco

const ProtocolVersion = 1

type Context struct {
	Workspace       string `json:"workspace"`
	Environment     string `json:"environment"`
	EnvironmentType string `json:"environmentType"`
	ProjectRoot     string `json:"projectRoot"`
}

type Manifest struct {
	Version int   `json:"version"`
	Stack   Stack `json:"stack"`
}

type Stack struct {
	Name     string    `json:"name"`
	Services []Service `json:"services"`
}

type Service struct {
	Key           string              `json:"key"`
	Name          string              `json:"name,omitempty"`
	Description   string              `json:"description,omitempty"`
	Build         *DockerBuild        `json:"build,omitempty"`
	Image         string              `json:"image,omitempty"`
	Routing       Routing             `json:"routing"`
	Domain        *PlatformDomain     `json:"domain,omitempty"`
	PrimaryRegion string              `json:"primaryRegion"`
	Regions       map[string]Region   `json:"regions"`
	Health        Health              `json:"health"`
	Env           map[string]Variable `json:"env,omitempty"`
	Observability Observability       `json:"observability"`
}

type DockerBuild struct {
	Context    string `json:"context"`
	Dockerfile string `json:"dockerfile"`
}

type Routing struct {
	Port        int32  `json:"port"`
	PathPrefix  string `json:"pathPrefix,omitempty"`
	IdleTimeout *int32 `json:"idleTimeout,omitempty"`
}

type PlatformDomain struct {
	Hostname string `json:"hostname"`
}

type Region struct {
	CPU         string       `json:"cpu"`
	Memory      string       `json:"memory"`
	ReplicasMin int32        `json:"replicasMin"`
	ReplicasMax int32        `json:"replicasMax"`
	Autoscaling *Autoscaling `json:"autoscaling,omitempty"`
}

type Autoscaling struct {
	CPUTarget    *int32 `json:"cpuTarget,omitempty"`
	MemoryTarget *int32 `json:"memoryTarget,omitempty"`
}

type Health struct {
	Path               string `json:"path"`
	Interval           int32  `json:"interval,omitempty"`
	Timeout            int32  `json:"timeout,omitempty"`
	StartupGracePeriod int32  `json:"startupGracePeriod,omitempty"`
	FailThreshold      int32  `json:"failThreshold,omitempty"`
}

type Observability struct {
	Logging Logging `json:"logging"`
	Metrics Metrics `json:"metrics"`
	Tracing Tracing `json:"tracing"`
}

type Logging struct {
	Enabled         *bool  `json:"enabled,omitempty"`
	RetentionPeriod string `json:"retentionPeriod,omitempty"`
	Structured      bool   `json:"structured,omitempty"`
}

type Metrics struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path,omitempty"`
	Port    int32  `json:"port,omitempty"`
}

type Tracing struct {
	Enabled    bool              `json:"enabled"`
	SampleRate *float64          `json:"sampleRate,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type VariableKind string

const (
	VariableLiteral  VariableKind = "literal"
	VariableSecret   VariableKind = "secret"
	VariablePreserve VariableKind = "preserve"
)

type Variable struct {
	Kind  VariableKind `json:"kind"`
	Value string       `json:"value,omitempty"`
	Name  string       `json:"name,omitempty"`
}

func Literal(value string) Variable {
	return Variable{Kind: VariableLiteral, Value: value}
}

func SecretRef(name string) Variable {
	return Variable{Kind: VariableSecret, Name: name}
}

func Preserve() Variable {
	return Variable{Kind: VariablePreserve}
}

func Value[T any](value T) *T {
	return &value
}
