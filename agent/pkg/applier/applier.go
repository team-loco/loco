package applier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	fieldOwner      = "loco-agent"
	applicationKind = "Application"
)

var (
	ErrInvalidPayload = errors.New("invalid payload")
	ErrStaleRevision  = errors.New("revision is older than the one applied")
	errNoServiceSpec  = errors.New("env secret needs a service deployment spec")

	errEnvSecretRevision = errors.New("env secret revision differs from the apply revision")
)

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
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add core types to scheme: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return NewWithClient(c, namespace), nil
}

// NewWithClient creates an Applier that writes through the given client.
func NewWithClient(c client.Client, namespace string) *Applier {
	return &Applier{client: c, namespace: namespace}
}

type EnvSecret struct {
	Revision int64
	Data     map[string][]byte
}

type Placement struct {
	ID          string
	Revision    int64
	ResourceID  string
	Application []byte
	EnvSecret   *EnvSecret
}

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
	if placement.EnvSecret != nil && placement.EnvSecret.Revision != placement.Revision {
		return fmt.Errorf("%w: %w: env secret %d, apply %d",
			ErrInvalidPayload, errEnvSecretRevision, placement.EnvSecret.Revision, placement.Revision)
	}
	if err := setEnvSecretRef(payload.AppSpec, placement); err != nil {
		return err
	}

	name := applicationName(placement.ResourceID)
	return retry.OnError(retry.DefaultRetry, isWriteRace, func() error {
		live, err := a.readPlacement(ctx, name, placement)
		if err != nil {
			return err
		}
		if placement.EnvSecret == nil {
			if applyErr := a.applyApplication(ctx, name, live, placement, payload.AppSpec); applyErr != nil {
				return applyErr
			}
			return a.deleteEnvSecret(ctx, placement)
		}
		if live.secretRevision == placement.Revision && live.ownsSecret() {
			return a.applyApplication(ctx, name, live, placement, payload.AppSpec)
		}
		if live.referencesSecret() {
			if writeErr := a.writeEnvSecret(ctx, live, placement); writeErr != nil {
				return writeErr
			}
			return a.applyApplication(ctx, name, live, placement, payload.AppSpec)
		}
		if applyErr := a.applyApplication(ctx, name, live, placement, payload.AppSpec); applyErr != nil {
			return applyErr
		}
		applied, getErr := a.getApplication(ctx, name)
		if getErr != nil {
			return getErr
		}
		live.application = applied
		return a.writeEnvSecret(ctx, live, placement)
	})
}

func isWriteRace(err error) bool {
	return apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err)
}

type livePlacement struct {
	application    *locoControllerV1.Application
	secret         *corev1.Secret
	secretRevision int64
}

func (l livePlacement) ownsSecret() bool {
	if l.application == nil || l.secret == nil {
		return false
	}
	return slices.ContainsFunc(l.secret.OwnerReferences, func(owner metav1.OwnerReference) bool {
		return owner.UID == l.application.UID
	})
}

func (l livePlacement) referencesSecret() bool {
	if l.application == nil || l.secret == nil {
		return false
	}
	spec := l.application.Spec.ServiceSpec
	return spec != nil && spec.Deployment != nil && spec.Deployment.EnvSecretRef != nil
}

func (a *Applier) readPlacement(ctx context.Context, name string, placement Placement) (livePlacement, error) {
	var live livePlacement
	app, err := a.getApplication(ctx, name)
	if client.IgnoreNotFound(err) != nil {
		return live, err
	}
	if err == nil {
		live.application = app
		stored, ok := PlacementOf(app)
		if ok && stored.ID == placement.ID && stored.Revision > placement.Revision {
			return live, fmt.Errorf("%w: have %d, got %d", ErrStaleRevision, stored.Revision, placement.Revision)
		}
	}
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: a.namespace, Name: locoControllerV1.EnvSecretName(placement.ID)}
	err = a.client.Get(ctx, key, secret)
	if apierrors.IsNotFound(err) {
		return live, nil
	}
	if err != nil {
		return live, fmt.Errorf("failed to read env Secret: %w", err)
	}
	live.secret = secret
	revision, ok := locoControllerV1.EnvSecretRevision(secret.Labels)
	if !ok {
		return live, nil
	}
	live.secretRevision = revision
	if placement.EnvSecret != nil && revision > placement.Revision {
		return live, fmt.Errorf("%w: env Secret has %d, got %d", ErrStaleRevision, revision, placement.Revision)
	}
	return live, nil
}

func (a *Applier) getApplication(ctx context.Context, name string) (*locoControllerV1.Application, error) {
	app := &locoControllerV1.Application{}
	key := client.ObjectKey{Namespace: a.namespace, Name: name}
	if err := a.client.Get(ctx, key, app); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return nil, err
		}
		return nil, fmt.Errorf("failed to read Application: %w", err)
	}
	return app, nil
}

func (a *Applier) applyApplication(
	ctx context.Context,
	name string,
	live livePlacement,
	placement Placement,
	spec *locoControllerV1.ApplicationSpec,
) error {
	resourceVersion := ""
	if live.application != nil {
		resourceVersion = live.application.ResourceVersion
	}
	applyConfig, err := applicationApplyConfiguration(name, a.namespace, resourceVersion, placement, spec)
	if err != nil {
		return err
	}
	if applyErr := a.client.Apply(
		ctx,
		applyConfig,
		client.FieldOwner(fieldOwner),
		client.ForceOwnership,
	); applyErr != nil {
		return fmt.Errorf("failed to apply Application: %w", applyErr)
	}
	slog.InfoContext(ctx, "applied Application",
		"name", name,
		"namespace", a.namespace,
		"placement_id", placement.ID,
		"revision", placement.Revision,
	)
	return nil
}

func setEnvSecretRef(spec *locoControllerV1.ApplicationSpec, placement Placement) error {
	if placement.EnvSecret == nil {
		return nil
	}
	if spec.ServiceSpec == nil || spec.ServiceSpec.Deployment == nil {
		return fmt.Errorf("%w: %w", ErrInvalidPayload, errNoServiceSpec)
	}
	spec.ServiceSpec.Deployment.EnvSecretRef = &locoControllerV1.EnvSecretRef{
		Name:     locoControllerV1.EnvSecretName(placement.ID),
		Revision: placement.EnvSecret.Revision,
	}
	return nil
}

func (a *Applier) deleteEnvSecret(ctx context.Context, placement Placement) error {
	stale := &corev1.Secret{}
	stale.Name = locoControllerV1.EnvSecretName(placement.ID)
	stale.Namespace = a.namespace
	err := a.client.Delete(ctx, stale)
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to delete env Secret: %w", err)
	}
	return nil
}

func (a *Applier) writeEnvSecret(ctx context.Context, live livePlacement, placement Placement) error {
	secret := live.secret
	if secret == nil {
		secret = &corev1.Secret{}
		secret.Name = locoControllerV1.EnvSecretName(placement.ID)
		secret.Namespace = a.namespace
	}
	revision := strconv.FormatInt(placement.EnvSecret.Revision, 10)
	secret.Labels = map[string]string{
		locoControllerV1.LabelPlacementID:       placement.ID,
		locoControllerV1.LabelPlacementRevision: revision,
	}
	secret.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: locoControllerV1.GroupVersion.String(),
		Kind:       applicationKind,
		Name:       live.application.Name,
		UID:        live.application.UID,
		Controller: new(true),
	}}
	secret.Type = corev1.SecretTypeOpaque
	secret.Data = placement.EnvSecret.Data
	if live.secret == nil {
		if err := a.client.Create(ctx, secret); err != nil {
			return fmt.Errorf("failed to create env Secret: %w", err)
		}
	} else if err := a.client.Update(ctx, secret); err != nil {
		return fmt.Errorf("failed to update env Secret: %w", err)
	}
	slog.InfoContext(ctx, "wrote env Secret",
		"name", secret.Name,
		"namespace", a.namespace,
		"placement_id", placement.ID,
		"revision", placement.EnvSecret.Revision,
	)
	return nil
}

// SweepEnvSecrets deletes the staging Secrets whose placement has no Application, or whose
// Application does not reference them.
func (a *Applier) SweepEnvSecrets(ctx context.Context) error {
	secrets := &corev1.SecretList{}
	hasPlacement := client.HasLabels{locoControllerV1.LabelPlacementID}
	if err := a.client.List(ctx, secrets, client.InNamespace(a.namespace), hasPlacement); err != nil {
		return fmt.Errorf("failed to list env Secrets: %w", err)
	}
	apps := &locoControllerV1.ApplicationList{}
	if err := a.client.List(ctx, apps, client.InNamespace(a.namespace)); err != nil {
		return fmt.Errorf("failed to list Applications: %w", err)
	}
	referenced := make(map[string]struct{}, len(apps.Items))
	for i := range apps.Items {
		spec := apps.Items[i].Spec.ServiceSpec
		if spec != nil && spec.Deployment != nil && spec.Deployment.EnvSecretRef != nil {
			referenced[spec.Deployment.EnvSecretRef.Name] = struct{}{}
		}
	}
	for i := range secrets.Items {
		secret := &secrets.Items[i]
		if _, ok := referenced[secret.Name]; ok {
			continue
		}
		precondition := client.Preconditions{ResourceVersion: &secret.ResourceVersion}
		err := a.client.Delete(ctx, secret, precondition)
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete orphaned env Secret %s: %w", secret.Name, err)
		}
		slog.InfoContext(ctx, "deleted orphaned env Secret", "name", secret.Name, "namespace", a.namespace)
	}
	return nil
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

	gvk := locoControllerV1.GroupVersion.WithKind(applicationKind)
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.SetResourceVersion(resourceVersion)
	revision := strconv.FormatInt(placement.Revision, 10)
	obj.SetAnnotations(map[string]string{
		locoControllerV1.AnnotationPlacementID:       placement.ID,
		locoControllerV1.AnnotationPlacementRevision: revision,
	})
	setErr := unstructured.SetNestedField(obj.Object, specMap, "spec")
	if setErr != nil {
		return nil, fmt.Errorf("%w: set spec: %w", ErrInvalidPayload, setErr)
	}

	return client.ApplyConfigurationFromUnstructured(obj), nil
}

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

func PlacementOf(app *locoControllerV1.Application) (Placement, bool) {
	id := app.PlacementID()
	if id == "" {
		return Placement{}, false
	}
	revision, ok := app.PlacementRevision()
	if !ok {
		return Placement{}, false
	}
	return Placement{ID: id, Revision: revision}, true
}

type DeployPayload struct {
	DeploymentID string                            `json:"deployment_id"`
	ResourceID   string                            `json:"resource_id"`
	WorkspaceID  string                            `json:"workspace_id"`
	ResourceName string                            `json:"resource_name"`
	ResourceType string                            `json:"resource_type"`
	Region       string                            `json:"region"`
	AppSpec      *locoControllerV1.ApplicationSpec `json:"app_spec"`
}
