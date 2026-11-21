package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourcesSpec contains CPU, Memory, replicas, and autoscaling
type ResourcesSpec struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	CPU string `json:"cpu"`
	// +required
	// +kubebuilder:validation:MinLength=1
	Memory string `json:"memory"`

	// +required
	Replicas ReplicasSpec `json:"replicas"`
	// +optional
	Scalers ScalersSpec `json:"scalers,omitzero"`
}

type ReplicasSpec struct {
	// +required
	// +kubebuilder:validation:Minimum=1
	Min int32 `json:"min"`
	// +required
	// +kubebuilder:validation:Minimum=1
	Max int32 `json:"max"`
}

type ScalersSpec struct {
	Enabled      bool  `json:"enabled,omitzero"`
	CPUTarget    int32 `json:"cpuTarget,omitzero"`
	MemoryTarget int32 `json:"memoryTarget,omitzero"`
}

// HealthCheckSpec describes readiness/liveness checks
type HealthCheckSpec struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`
	// +required
	// +kubebuilder:validation:Minimum=1
	Interval int32 `json:"interval"` // seconds
	// +required
	// +kubebuilder:validation:Minimum=1
	Timeout int32 `json:"timeout"` // seconds
	// +required
	// +kubebuilder:validation:Minimum=1
	FailThreshold int32 `json:"failThreshold"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	StartupGracePeriod int32 `json:"startupGracePeriod,omitzero"` // seconds
}

// RoutingSpec exposes the service on the gateway at HostName.
type RoutingSpec struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	HostName string `json:"hostName"`
	// +required
	// +kubebuilder:validation:MinLength=1
	PathPrefix string `json:"pathPrefix"`
	// +required
	// +kubebuilder:validation:Minimum=1
	IdleTimeout int32 `json:"idleTimeout"` // seconds
}

// ApplicationSpec defines the desired state of Application
type ApplicationSpec struct {
	// Type is the resource type. SERVICE is the only one.
	Type           string `json:"type"`
	ResourceID     string `json:"resourceId,omitempty"` // UUIDv7 as string
	Slug           string `json:"slug,omitempty"`
	OrganizationID string `json:"organizationId,omitempty"` // UUIDv7 as string
	WorkspaceID    string `json:"workspaceId,omitempty"`    // UUIDv7 as string
	Region         string `json:"region,omitempty"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="environmentId is immutable"
	EnvironmentID   string `json:"environmentId"`             // UUIDv7 as string
	EnvironmentName string `json:"environmentName,omitempty"` // e.g. "production", "staging"
	DeploymentID    string `json:"deploymentId,omitempty"`    // UUIDv7 as string

	ServiceSpec *ServiceSpec `json:"serviceSpec,omitempty"`
}

// ServiceSpec contains service-specific deployment and resource configuration
type ServiceSpec struct {
	// Deployment info (current or requested)
	// +required
	Deployment *ServiceDeploymentSpec `json:"deployment"`

	// Resources (CPU, Memory, Replicas, Scalers)
	// +required
	Resources *ResourcesSpec `json:"resources"`

	// Routing exposes the service to the internet. Without it the service gets no HTTPRoute and no gateway ingress.
	Routing *RoutingSpec `json:"routing,omitempty"`

	// +required
	RestartPolicy RestartPolicySpec `json:"restartPolicy"`

	// DeployTimeoutSeconds is how long a new placement revision has to become healthy before Loco stops it.
	// +required
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=3600
	DeployTimeoutSeconds int32 `json:"deployTimeoutSeconds"`
}

// +kubebuilder:validation:Enum=on-failure;always;never
type RestartMode string

const (
	RestartOnFailure RestartMode = "on-failure"
	RestartAlways    RestartMode = "always"
	RestartNever     RestartMode = "never"
)

// RestartPolicySpec decides when a replica's exits stop the revision. Under on-failure, the exit past
// MaxRestarts before the replica stays up for the controller's health window stops it; under never,
// the first exit; under always, none.
type RestartPolicySpec struct {
	// +required
	Mode RestartMode `json:"mode"`
	// MaxRestarts is the on-failure budget.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=5
	MaxRestarts int32 `json:"maxRestarts,omitzero"`
}

// ObservabilityConfig controls automatic application SDK environment configuration.
type ObservabilityConfig struct {
	// InjectOtel supplies SDK environment defaults for each variable the application does not set itself.
	// +optional
	// +kubebuilder:default=true
	InjectOtel bool `json:"injectOtel"`
}

// ServiceDeploymentSpec contains service deployment-specific configuration
type ServiceDeploymentSpec struct {
	// +kubebuilder:validation:Enum=linux/amd64;linux/arm64
	Platform string `json:"platform"`

	// +optional
	// +kubebuilder:default={}
	Observability ObservabilityConfig `json:"observability"`

	// +required
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`
	// Port is the port the service listens on. Without it the service is a worker: it gets no
	// Service, no route and no readiness probe.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port *int32 `json:"port,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483647
	RunAsUser *int64 `json:"runAsUser,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483647
	RunAsGroup *int64 `json:"runAsGroup,omitempty"`

	// Command replaces the image ENTRYPOINT. Without it the image's ENTRYPOINT runs.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:items:MinLength=1
	Command []string `json:"command,omitempty"`
	// Args replaces the image CMD. Without it the image's CMD is passed to the entrypoint.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	Args []string `json:"args,omitempty"`

	HealthCheck  *HealthCheckSpec  `json:"healthCheck,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	EnvSecretRef *EnvSecretRef     `json:"envSecretRef,omitempty"`
}

// EnvSecretRef names the Secret in the Application's namespace that holds the service's secret values
// and the placement revision whose values it must carry.
type EnvSecretRef struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +required
	// +kubebuilder:validation:Minimum=1
	Revision int64 `json:"revision"`
}

// ApplicationStatus defines the observed state of Application.
type ApplicationStatus struct {
	ReadyReplicas int32 `json:"readyReplicas,omitzero"`

	Rollout *RolloutStatus `json:"rollout,omitempty"`

	ObservedPlacementRevision int64  `json:"observedPlacementRevision,omitzero"`
	ObservedDeploymentID      string `json:"observedDeploymentId,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Application is the Schema for the applications API
type Application struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Application
	// +required
	Spec ApplicationSpec `json:"spec"`

	// status defines the observed state of Application
	// +optional
	Status ApplicationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ApplicationList contains a list of Application
type ApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Application `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Application{}, &ApplicationList{})
}
