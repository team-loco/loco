package kube

import (
	"encoding/json"
	"fmt"
	"time"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/shared/config"
)

// LocoDeploymentContext wraps App and Deployment for K8s operations
type LocoDeploymentContext struct {
	App        *genDb.App
	Deployment *genDb.Deployment
	Config     *config.AppConfig
}

// DockerRegistryConfig for creating docker pull secrets
type DockerRegistryConfig struct {
	Server   string
	Username string
	Password string
	Email    string
}

// Kubernetes labels
const (
	LabelAppName       = "app.loco.io/name"
	LabelAppInstance   = "app.loco.io/instance"
	LabelAppVersion    = "app.loco.io/version"
	LabelAppComponent  = "app.loco.io/component"
	LabelAppPartOf     = "app.loco.io/part-of"
	LabelAppManagedBy  = "app.loco.io/managed-by"
	LabelAppCreatedFor = "app.loco.io/created-for"
	LabelAppCreatedAt  = "app.loco.io/created-at"

	DefaultReplicas        = 1
	DefaultServicePort     = 80
	DefaultRequestTimeout  = "30s"
	MaxSurgePercent        = "25%"
	MaxUnavailablePercent  = "25%"
	MaxReplicaHistory      = 10
	SessionAffinityTimeout = 10800 // 3 hours
	TerminationGracePeriod = 30
	LocoGatewayName        = "loco-gateway"
	LocoNS                 = "loco-system"

	// Probe constants
	DefaultStartupGracePeriod = 30
	DefaultTimeout            = 5
	DefaultInterval           = 10
	DefaultFailureThreshold   = 3

	// Default Resource constants
	DefaultCPU    = "100m"
	DefaultMemory = "128Mi"

	// TimeFormat
	DefaultTimeFormat = "2006-01-02T15:04:05-0700"
)

// UnmarshalConfig unmarshals AppRevision.Config JSON into AppConfig
func UnmarshalConfig(configBytes []byte) (*config.AppConfig, error) {
	var cfg config.AppConfig
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal app config: %w", err)
	}
	return &cfg, nil
}

// NewLocoDeploymentContext creates a context from DB models
func NewLocoDeploymentContext(app *genDb.App, deployment *genDb.Deployment) (*LocoDeploymentContext, error) {
	cfg, err := UnmarshalConfig(deployment.Config)
	if err != nil {
		return nil, err
	}

	return &LocoDeploymentContext{
		App:        app,
		Deployment: deployment,
		Config:     cfg,
	}, nil
}

// Namespace returns the namespace name for this deployment
func (ldc *LocoDeploymentContext) Namespace() string {
	return fmt.Sprintf("wks-%d-app-%d", ldc.App.WorkspaceID, ldc.App.ID)
}

// DeploymentName returns the K8s deployment name
func (ldc *LocoDeploymentContext) DeploymentName() string {
	return ldc.App.Name
}

// ServiceName returns the K8s service name
func (ldc *LocoDeploymentContext) ServiceName() string {
	return ldc.App.Name
}

// ServicePort returns the K8s service port name
func (ldc *LocoDeploymentContext) ServicePort() string {
	return ldc.App.Name
}

// EnvSecretName returns the secret name for environment variables
func (ldc *LocoDeploymentContext) EnvSecretName() string {
	return ldc.App.Name
}

// RegistrySecretName returns the secret name for docker registry credentials
func (ldc *LocoDeploymentContext) RegistrySecretName() string {
	return fmt.Sprintf("%s-registry-credentials", ldc.App.Name)
}

// ServiceAccountName returns the K8s service account name
func (ldc *LocoDeploymentContext) ServiceAccountName() string {
	return ldc.App.Name
}

// RoleName returns the K8s role name
func (ldc *LocoDeploymentContext) RoleName() string {
	return ldc.App.Name
}

// RoleBindingName returns the K8s role binding name
func (ldc *LocoDeploymentContext) RoleBindingName() string {
	return ldc.App.Name
}

// HTTPRouteName returns the K8s gateway HTTPRoute name
func (ldc *LocoDeploymentContext) HTTPRouteName() string {
	return ldc.App.Name
}

// ContainerName returns the container name
func (ldc *LocoDeploymentContext) ContainerName() string {
	return ldc.App.Name
}

// Labels generates K8s labels for this deployment
func (ldc *LocoDeploymentContext) Labels() map[string]string {
	return map[string]string{
		LabelAppName:       ldc.App.Name,
		LabelAppInstance:   ldc.Namespace(),
		LabelAppVersion:    "1.0.0",
		LabelAppComponent:  "backend",
		LabelAppPartOf:     "loco-platform",
		LabelAppManagedBy:  "loco",
		LabelAppCreatedFor: fmt.Sprintf("%d", ldc.App.CreatedBy),
		LabelAppCreatedAt:  time.Now().UTC().Format("20060102T150405Z"),
	}
}

// Helper functions for pointer conversion
func ptrToBool(b bool) *bool {
	return &b
}

func ptrToInt32(i int32) *int32 {
	return &i
}

func ptrToInt64(i int64) *int64 {
	return &i
}

func ptrToString(s string) *string {
	return &s
}
