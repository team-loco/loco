/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ResourcesSpec contains CPU, Memory, replicas, and autoscaling
type ResourcesSpec struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`

	Replicas ReplicasSpec `json:"replicas,omitempty"`
	Scalers  ScalersSpec  `json:"scalers,omitempty"`
}

type ReplicasSpec struct {
	Min int32 `json:"min,omitempty"`
	Max int32 `json:"max,omitempty"`
}

type ScalersSpec struct {
	Enabled      bool  `json:"enabled,omitempty"`
	CPUTarget    int32 `json:"cpuTarget,omitempty"`
	MemoryTarget int32 `json:"memoryTarget,omitempty"`
}

// HealthCheckSpec describes readiness/liveness checks
type HealthCheckSpec struct {
	Path               string `json:"path,omitempty"`
	Interval           int32  `json:"interval,omitempty"` // seconds
	Timeout            int32  `json:"timeout,omitempty"`  // seconds
	FailThreshold      int32  `json:"failThreshold,omitempty"`
	StartupGracePeriod int32  `json:"startupGracePeriod,omitempty"` // seconds
}

// MetricsSpec defines metrics scraping info
type MetricsSpec struct {
	Enabled bool   `json:"enabled,omitempty"`
	Path    string `json:"path,omitempty"`
	Port    int32  `json:"port,omitempty"`
}

// ObsSpec contains logging, metrics, tracing
type ObsSpec struct {
	Logging LoggingSpec `json:"logging,omitzero"`
	Metrics MetricsSpec `json:"metrics,omitzero"`
	Tracing TracingSpec `json:"tracing,omitzero"`
}

type LoggingSpec struct {
	Enabled         bool   `json:"enabled,omitempty"`
	RetentionPeriod string `json:"retentionPeriod,omitempty"` // e.g. 7d
	Structured      bool   `json:"structured,omitempty"`
}

type TracingSpec struct {
	Enabled    bool              `json:"enabled,omitempty"`
	SampleRate string            `json:"sampleRate,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

// RoutingSpec contains subdomain, path prefix, port, idle timeout
type RoutingSpec struct {
	HostName    string `json:"hostName,omitempty"`
	PathPrefix  string `json:"pathPrefix,omitempty"`
	IdleTimeout int32  `json:"idleTimeout,omitempty"` // seconds
}

// ApplicationSpec defines the desired state of Application
// Uses a type discriminator with type-specific specs to support multiple resource types
type ApplicationSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// Type indicates the resource type (SERVICE, DATABASE, CACHE, QUEUE, BLOB)
	// Only the corresponding TypeSpec field should be populated
	Type        string `json:"type"`                 // SERVICE, DATABASE, CACHE, QUEUE, BLOB
	ResourceId  int64  `json:"resourceId,omitempty"` // optional
	WorkspaceId int64  `json:"workspaceId,omitempty"`

	// Type-specific specs (only one populated based on Type)
	ServiceSpec  *ServiceSpec  `json:"serviceSpec,omitempty"`
	DatabaseSpec *DatabaseSpec `json:"databaseSpec,omitempty"`
	CacheSpec    *CacheSpec    `json:"cacheSpec,omitempty"`
	QueueSpec    *QueueSpec    `json:"queueSpec,omitempty"`
	BlobSpec     *BlobSpec     `json:"blobSpec,omitempty"`
}

// ServiceSpec contains service-specific deployment and resource configuration
type ServiceSpec struct {
	// Deployment info (current or requested)
	Deployment *ServiceDeploymentSpec `json:"deployment,omitempty"`

	// Resources (CPU, Memory, Replicas, Scalers) - global defaults
	Resources *ResourcesSpec `json:"resources,omitempty"`

	// Routing configuration (port, domain, subdomain, etc)
	Routing *RoutingSpec `json:"routing,omitempty"`

	// Observability configuration (logging, metrics, tracing)
	Obs *ObsSpec `json:"obs,omitempty"`
}

// ServiceDeploymentSpec contains service deployment-specific configuration
type ServiceDeploymentSpec struct {
	Image          string `json:"image,omitempty"`
	Port           int32  `json:"port,omitempty"`
	DockerfilePath string `json:"dockerfilePath,omitempty"`
	BuildType      string `json:"buildType,omitempty"` // docker, buildpack, etc

	// Resource requests (defaults from resource if omitted)
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`

	// Replica configuration (defaults from resource if omitted)
	MinReplicas int32 `json:"minReplicas,omitempty"`
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

	// Autoscaling (defaults from resource if omitted)
	Scalers *ScalersSpec `json:"scalers,omitempty"`

	HealthCheck *HealthCheckSpec  `json:"healthCheck,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// DatabaseSpec is a placeholder for future DATABASE type resources
type DatabaseSpec struct {
	// TODO: Add when implementing database support
	// Engine      string `json:"engine,omitempty"`      // "postgres", "mysql"
	// Version     string `json:"version,omitempty"`     // "15"
	// Storage     *StorageSpec `json:"storage,omitempty"`
	// Replication *ReplicationSpec `json:"replication,omitempty"`
}

// CacheSpec is a placeholder for future CACHE type resources
type CacheSpec struct {
	// TODO: Add when implementing cache support
}

// QueueSpec is a placeholder for future QUEUE type resources
type QueueSpec struct {
	// TODO: Add when implementing queue support
}

// BlobSpec is a placeholder for future BLOB type resources
type BlobSpec struct {
	// TODO: Add when implementing blob storage support
}

// ApplicationStatus defines the observed state of Application.
type ApplicationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the Application resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.

	// +kubebuilder:validation:Enum=Idle;Deploying;Ready;Failed
	Phase               string `json:"phase,omitempty"` // Idle | Deploying | Ready | Failed
	Message             string `json:"message,omitempty"`
	ActiveDeploymentRef string `json:"activeDeploymentRef,omitempty"`

	CreatedAt   *metav1.Time `json:"createdAt,omitempty"`
	StartedAt   *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
	UpdatedAt   *metav1.Time `json:"updatedAt,omitempty"`

	DeployedGeneration int64 `json:"deployedGeneration,omitempty"` // tracks spec changes applied

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
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
