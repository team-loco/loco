package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	BuildPhasePending   = "Pending"
	BuildPhaseRunning   = "Running"
	BuildPhaseSucceeded = "Succeeded"
	BuildPhaseFailed    = "Failed"
	BuildPhaseCanceled  = "Canceled"
)

type BuildSpec struct {
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	BuildID string `json:"buildId"`

	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	WorkspaceID string `json:"workspaceId"`

	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	ResourceID string `json:"resourceId"`

	// +kubebuilder:validation:MaxLength=4096
	// +kubebuilder:validation:Pattern=`^https?://`
	SourceURL string `json:"sourceURL"`

	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('/') && !self.endsWith('/') && !self.split('/').exists(p, p == '..' || p == '.' || p == '')",message="dockerfilePath must be a relative path inside the build context"
	DockerfilePath string `json:"dockerfilePath"`

	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)+$`
	ImageRepository string `json:"imageRepository"`

	// +optional
	// +kubebuilder:validation:MaxLength=330
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)+@sha256:[a-f0-9]{64}$`
	CacheRef string `json:"cacheRef,omitempty"`
}

type BuildStatus struct {
	// +optional
	// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed;Canceled
	Phase string `json:"phase,omitempty"`

	// +optional
	ImageDigest string `json:"imageDigest,omitempty"`

	// +optional
	CacheDigest string `json:"cacheDigest,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`

	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.status.imageDigest`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="self.metadata.name.size() <= 63",message="metadata.name must be at most 63 characters"
type Build struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
	Spec BuildSpec `json:"spec"`

	// +optional
	Status BuildStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true
type BuildList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Build `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Build{}, &BuildList{})
}

func (b *Build) Finished() bool {
	switch b.Status.Phase {
	case BuildPhaseSucceeded:
		return true
	case BuildPhaseFailed:
		return true
	case BuildPhaseCanceled:
		return true
	default:
		return false
	}
}
