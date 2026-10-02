package applier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const fieldOwner = "loco-agent"

var ErrInvalidPayload = errors.New("invalid payload")

// Applier handles applying Kubernetes resources.
type Applier struct {
	client    client.Client
	namespace string
}

// New creates a new Applier for the given cluster config.
func New(cfg *rest.Config, namespace string) (*Applier, error) {
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add loco types to scheme: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &Applier{client: c, namespace: namespace}, nil
}

// ApplyFromJSON server-side applies an Application from a deploy payload.
func (a *Applier) ApplyFromJSON(ctx context.Context, specJSON []byte) error {
	var payload DeployPayload
	if err := json.Unmarshal(specJSON, &payload); err != nil {
		return fmt.Errorf("%w: unmarshal deploy payload: %w", ErrInvalidPayload, err)
	}
	if payload.ResourceID == "" {
		return fmt.Errorf("%w: deploy payload has no resource_id", ErrInvalidPayload)
	}
	if payload.AppSpec == nil {
		return fmt.Errorf("%w: deploy payload has no app_spec", ErrInvalidPayload)
	}

	slog.InfoContext(ctx, "applying application",
		"resource_id", payload.ResourceID,
		"resource_name", payload.ResourceName,
		"namespace", a.namespace,
	)

	name := applicationName(payload.ResourceID)
	applyConfig, err := applicationApplyConfiguration(name, a.namespace, payload.AppSpec)
	if err != nil {
		return err
	}

	applyErr := a.client.Apply(ctx, applyConfig, client.FieldOwner(fieldOwner), client.ForceOwnership)
	if applyErr != nil {
		return fmt.Errorf("failed to apply Application: %w", applyErr)
	}

	slog.InfoContext(ctx, "applied Application", "name", name, "namespace", a.namespace)
	return nil
}

func applicationName(resourceID string) string {
	return "resource-" + resourceID
}

func applicationApplyConfiguration(
	name, namespace string,
	spec *locoControllerV1.ApplicationSpec,
) (runtime.ApplyConfiguration, error) {
	specMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(spec)
	if err != nil {
		return nil, fmt.Errorf("%w: convert app_spec: %w", ErrInvalidPayload, err)
	}

	gvk := locoControllerV1.GroupVersion.WithKind("Application")
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	setErr := unstructured.SetNestedField(obj.Object, specMap, "spec")
	if setErr != nil {
		return nil, fmt.Errorf("%w: set spec: %w", ErrInvalidPayload, setErr)
	}

	return client.ApplyConfigurationFromUnstructured(obj), nil
}

// DeleteFromJSON deletes an Application by resource ID.
func (a *Applier) DeleteFromJSON(ctx context.Context, resourceID string) error {
	slog.InfoContext(ctx, "deleting application", "resource_id", resourceID, "namespace", a.namespace)

	if resourceID == "" {
		return fmt.Errorf("%w: delete payload has no resource_id", ErrInvalidPayload)
	}

	app := &locoControllerV1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      applicationName(resourceID),
			Namespace: a.namespace,
		},
	}

	if err := a.client.Delete(ctx, app); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete Application: %w", err)
		}
		slog.WarnContext(ctx, "Application not found for deletion", "name", app.Name)
		return nil
	}

	slog.InfoContext(ctx, "deleted Application", "name", app.Name, "namespace", a.namespace)
	return nil
}

// DeployPayload matches the structure sent by the API's DeployCommandPayload.
type DeployPayload struct {
	DeploymentID string                            `json:"deployment_id"`
	ResourceID   string                            `json:"resource_id"`
	WorkspaceID  string                            `json:"workspace_id"`
	ResourceName string                            `json:"resource_name"`
	ResourceType string                            `json:"resource_type"`
	Region       string                            `json:"region"`
	Hostname     string                            `json:"hostname"`
	AppSpec      *locoControllerV1.ApplicationSpec `json:"app_spec"`
}
