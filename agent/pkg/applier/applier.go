package applier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
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

const (
	AnnotationPlacementID       = "loco.io/placement-id"
	AnnotationPlacementRevision = "loco.io/placement-revision"
)

var ErrStaleRevision = errors.New("revision is older than the one applied")

type Placement struct {
	ID          string
	Revision    int64
	ResourceID  string
	Application []byte
}

// ApplyPlacement server-side applies the Application for a placement revision.
func (a *Applier) ApplyPlacement(ctx context.Context, placement Placement) error {
	var payload DeployPayload
	if err := json.Unmarshal(placement.Application, &payload); err != nil {
		return fmt.Errorf("%w: unmarshal application: %w", ErrInvalidPayload, err)
	}
	if placement.ID == "" {
		return fmt.Errorf("%w: placement has no id", ErrInvalidPayload)
	}
	if placement.ResourceID == "" {
		return fmt.Errorf("%w: placement has no resource_id", ErrInvalidPayload)
	}
	if payload.AppSpec == nil {
		return fmt.Errorf("%w: application has no app_spec", ErrInvalidPayload)
	}

	name := applicationName(placement.ResourceID)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		resourceVersion, err := a.checkRevision(ctx, name, placement)
		if err != nil {
			return err
		}
		applyConfig, err := applicationApplyConfiguration(
			name,
			a.namespace,
			resourceVersion,
			placement,
			payload.AppSpec,
		)
		if err != nil {
			return err
		}
		applyErr := a.client.Apply(ctx, applyConfig, client.FieldOwner(fieldOwner), client.ForceOwnership)
		if applyErr != nil {
			return fmt.Errorf("failed to apply Application: %w", applyErr)
		}
		slog.InfoContext(ctx, "applied Application",
			"name", name,
			"namespace", a.namespace,
			"placement_id", placement.ID,
			"revision", placement.Revision,
		)
		return nil
	})
}

func (a *Applier) checkRevision(ctx context.Context, name string, placement Placement) (string, error) {
	live := &locoControllerV1.Application{}
	key := client.ObjectKey{Namespace: a.namespace, Name: name}
	if err := a.client.Get(ctx, key, live); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return "", nil
		}
		return "", fmt.Errorf("failed to read Application: %w", err)
	}
	stored, ok := PlacementOf(live)
	if ok && stored.ID == placement.ID && stored.Revision > placement.Revision {
		return "", fmt.Errorf("%w: have %d, got %d", ErrStaleRevision, stored.Revision, placement.Revision)
	}
	return live.ResourceVersion, nil
}

func applicationName(resourceID string) string {
	return "resource-" + resourceID
}

func applicationApplyConfiguration(
	name, namespace, resourceVersion string,
	placement Placement,
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
	obj.SetResourceVersion(resourceVersion)
	revision := strconv.FormatInt(placement.Revision, 10)
	obj.SetAnnotations(map[string]string{
		AnnotationPlacementID:       placement.ID,
		AnnotationPlacementRevision: revision,
	})
	setErr := unstructured.SetNestedField(obj.Object, specMap, "spec")
	if setErr != nil {
		return nil, fmt.Errorf("%w: set spec: %w", ErrInvalidPayload, setErr)
	}

	return client.ApplyConfigurationFromUnstructured(obj), nil
}

// DeletePlacement deletes the Application for a placement unless a different placement owns it.
func (a *Applier) DeletePlacement(ctx context.Context, placement Placement) error {
	if placement.ResourceID == "" {
		return fmt.Errorf("%w: delete has no resource_id", ErrInvalidPayload)
	}
	name := applicationName(placement.ResourceID)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		live := &locoControllerV1.Application{}
		key := client.ObjectKey{Namespace: a.namespace, Name: name}
		if err := a.client.Get(ctx, key, live); err != nil {
			if client.IgnoreNotFound(err) == nil {
				return nil
			}
			return fmt.Errorf("failed to read Application: %w", err)
		}

		stored, ok := PlacementOf(live)
		if ok && stored.ID != placement.ID {
			slog.InfoContext(ctx, "Application belongs to another placement, not deleting",
				"name", name,
				"placement_id", placement.ID,
				"owner_placement_id", stored.ID,
			)
			return nil
		}
		if ok && stored.Revision > placement.Revision {
			return fmt.Errorf("%w: have %d, got %d", ErrStaleRevision, stored.Revision, placement.Revision)
		}

		precondition := client.Preconditions{ResourceVersion: &live.ResourceVersion}
		if err := a.client.Delete(ctx, live, precondition); err != nil {
			if client.IgnoreNotFound(err) == nil {
				return nil
			}
			return fmt.Errorf("failed to delete Application: %w", err)
		}
		slog.InfoContext(
			ctx,
			"deleted Application",
			"name",
			name,
			"namespace",
			a.namespace,
			"placement_id",
			placement.ID,
		)
		return nil
	})
}

// PlacementOf reads the placement annotations of an Application.
func PlacementOf(app *locoControllerV1.Application) (Placement, bool) {
	annotations := app.GetAnnotations()
	id := annotations[AnnotationPlacementID]
	if id == "" {
		return Placement{}, false
	}
	revision, err := strconv.ParseInt(annotations[AnnotationPlacementRevision], 10, 64)
	if err != nil {
		return Placement{}, false
	}
	return Placement{ID: id, Revision: revision}, true
}

// DeployPayload matches the structure sent by the API's ApplicationPayload.
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
