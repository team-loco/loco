package applier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Applier handles applying Kubernetes resources.
type Applier struct {
	client client.Client
}

// New creates a new Applier using in-cluster config.
func New() (*Applier, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to default kubeconfig for local development
		slog.Warn("not running in cluster, trying default kubeconfig", "error", err)
		cfg, err = getOutOfClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get kubernetes config: %w", err)
		}
	}

	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add loco types to scheme: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &Applier{client: c}, nil
}

// getOutOfClusterConfig loads kubeconfig from the default location.
func getOutOfClusterConfig() (*rest.Config, error) {
	// Try KUBECONFIG env var first, then default location
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

// ApplyFromJSON applies an Application from JSON spec.
func (a *Applier) ApplyFromJSON(ctx context.Context, specJSON []byte) error {
	// Parse the deploy command payload
	var payload DeployPayload
	if err := json.Unmarshal(specJSON, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal deploy payload: %w", err)
	}

	slog.Info("applying application",
		"resource_id", payload.ResourceID,
		"resource_name", payload.ResourceName,
		"namespace", payload.LocoNamespace,
	)

	// Build the Application CR
	app := &locoControllerV1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("resource-%s", payload.ResourceID),
			Namespace: payload.LocoNamespace,
			Labels:    map[string]string{},
		},
		Spec: *payload.AppSpec,
	}

	// Check if it exists
	existing := &locoControllerV1.Application{}
	err := a.client.Get(ctx, client.ObjectKey{
		Name:      app.Name,
		Namespace: app.Namespace,
	}, existing)

	if err == nil {
		// Update existing
		existing.Spec = app.Spec
		if err := a.client.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update Application: %w", err)
		}
		slog.Info("updated Application", "name", app.Name, "namespace", app.Namespace)
	} else if client.IgnoreNotFound(err) == nil {
		// Create new
		if err := a.client.Create(ctx, app); err != nil {
			return fmt.Errorf("failed to create Application: %w", err)
		}
		slog.Info("created Application", "name", app.Name, "namespace", app.Namespace)
	} else {
		return fmt.Errorf("failed to check Application existence: %w", err)
	}

	return nil
}

// DeleteFromJSON deletes an Application by resource ID.
func (a *Applier) DeleteFromJSON(ctx context.Context, resourceID string, namespace string) error {
	slog.Info("deleting application", "resource_id", resourceID, "namespace", namespace)

	app := &locoControllerV1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("resource-%v", resourceID),
			Namespace: namespace,
		},
	}

	if err := a.client.Delete(ctx, app); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete Application: %w", err)
		}
		slog.Warn("Application not found for deletion", "name", app.Name)
	}

	slog.Info("deleted Application", "name", app.Name, "namespace", namespace)
	return nil
}

// DeployPayload matches the structure sent by the API's DeployCommandPayload.
type DeployPayload struct {
	DeploymentID  string                            `json:"deployment_id"`
	ResourceID    string                            `json:"resource_id"`
	WorkspaceID   string                            `json:"workspace_id"`
	ResourceName  string                            `json:"resource_name"`
	ResourceType  string                            `json:"resource_type"`
	Region        string                            `json:"region"`
	Hostname      string                            `json:"hostname"`
	LocoNamespace string                            `json:"loco_namespace"`
	AppSpec       *locoControllerV1.ApplicationSpec `json:"app_spec"`
}

// DeletePayload matches the structure sent by the API's DeleteCommandPayload.
type DeletePayload struct {
	DeploymentID  string `json:"deployment_id"`
	ResourceID    string `json:"resource_id"`
	LocoNamespace string `json:"loco_namespace"`
}
