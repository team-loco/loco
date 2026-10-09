package applier

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testNamespace  = "loco-system"
	testResourceID = "abc"
	testAppType    = "SERVICE"
)

func newTestApplier(t *testing.T, existing ...client.Object) *Applier {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing...).Build()
	return &Applier{client: c, namespace: testNamespace}
}

func application(t *testing.T) []byte {
	t.Helper()
	return marshalPayload(t, &locoControllerV1.ApplicationSpec{Type: testAppType, ResourceID: testResourceID})
}

func serviceApplication(t *testing.T) []byte {
	t.Helper()
	return marshalPayload(t, &locoControllerV1.ApplicationSpec{
		Type:       testAppType,
		ResourceID: testResourceID,
		ServiceSpec: &locoControllerV1.ServiceSpec{
			Deployment: &locoControllerV1.ServiceDeploymentSpec{Image: "ghcr.io/team-loco/app:v1", Port: 8080},
		},
	})
}

func marshalPayload(t *testing.T, spec *locoControllerV1.ApplicationSpec) []byte {
	t.Helper()
	payload, err := json.Marshal(DeployPayload{ResourceID: spec.ResourceID, AppSpec: spec})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return payload
}

func (a *Applier) envSecret(t *testing.T) (*corev1.Secret, error) {
	t.Helper()
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: testNamespace, Name: locoControllerV1.EnvSecretName(testPlacementID)}
	err := a.client.Get(context.Background(), key, secret)
	return secret, err
}

func TestApplyPlacementWritesTheEnvSecretOwnedByTheApplication(t *testing.T) {
	ctx := context.Background()
	existing := &locoControllerV1.Application{
		Name: "resource-abc", Namespace: testNamespace, UID: "app-uid",
		Spec: locoControllerV1.ApplicationSpec{Type: testAppType, ResourceID: testResourceID},
	}
	a := newTestApplier(t, existing)
	value := []byte("postgres://user:pass@db/app")
	withSecret := placement(t, "p1", 2)
	withSecret.Application = serviceApplication(t)
	withSecret.EnvSecret = &EnvSecret{Revision: 2, Data: map[string][]byte{"DATABASE_URL": value}}

	if err := a.ApplyPlacement(ctx, withSecret); err != nil {
		t.Fatalf("apply with env secret: %v", err)
	}
	app, err := a.live(t)
	if err != nil {
		t.Fatalf("get Application: %v", err)
	}
	ref := app.Spec.ServiceSpec.Deployment.EnvSecretRef
	if ref == nil || ref.Name != "env-p1" || ref.Revision != 2 {
		t.Fatalf("envSecretRef = %+v, want env-p1 at revision 2", ref)
	}
	if app.Spec.ServiceSpec.Deployment.Env != nil {
		t.Fatalf("the Application carries env values: %v", app.Spec.ServiceSpec.Deployment.Env)
	}
	secret, err := a.envSecret(t)
	if err != nil {
		t.Fatalf("get env Secret: %v", err)
	}
	if string(secret.Data["DATABASE_URL"]) != string(value) {
		t.Fatalf("env Secret data = %v, want DATABASE_URL", secret.Data)
	}
	if secret.Labels[locoControllerV1.LabelPlacementID] != "p1" ||
		secret.Labels[locoControllerV1.LabelPlacementRevision] != "2" {
		t.Fatalf("env Secret labels = %v, want placement p1 at revision 2", secret.Labels)
	}
	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Kind != applicationKind ||
		secret.OwnerReferences[0].Name != app.Name || secret.OwnerReferences[0].UID != existing.UID {
		t.Fatalf("env Secret owners = %+v, want the Application %s (%s)", secret.OwnerReferences, app.Name, app.UID)
	}

	withoutSecret := placement(t, "p1", 3)
	withoutSecret.Application = serviceApplication(t)
	if applyErr := a.ApplyPlacement(ctx, withoutSecret); applyErr != nil {
		t.Fatalf("apply without env secret: %v", applyErr)
	}
	if _, getErr := a.envSecret(t); !apierrors.IsNotFound(getErr) {
		t.Fatalf("env Secret after a placement without secrets: err = %v, want not found", getErr)
	}
	app, err = a.live(t)
	if err != nil {
		t.Fatalf("get Application: %v", err)
	}
	if app.Spec.ServiceSpec.Deployment.EnvSecretRef != nil {
		t.Fatalf("envSecretRef = %+v after a placement without secrets", app.Spec.ServiceSpec.Deployment.EnvSecretRef)
	}
}

func placement(t *testing.T, id string, revision int64) Placement {
	t.Helper()
	return Placement{
		ID:          id,
		Revision:    revision,
		ResourceID:  testResourceID,
		Application: application(t),
	}
}

func (a *Applier) live(t *testing.T) (*locoControllerV1.Application, error) {
	t.Helper()
	app := &locoControllerV1.Application{}
	key := client.ObjectKey{Namespace: testNamespace, Name: "resource-abc"}
	err := a.client.Get(context.Background(), key, app)
	return app, err
}

func TestApplyPlacementRecordsRevisionAndRejectsOlderOnes(t *testing.T) {
	ctx := context.Background()
	a := newTestApplier(t)

	if err := a.ApplyPlacement(ctx, placement(t, "p1", 2)); err != nil {
		t.Fatalf("apply revision 2: %v", err)
	}
	app, err := a.live(t)
	if err != nil {
		t.Fatalf("get applied Application: %v", err)
	}
	got, ok := PlacementOf(app)
	if !ok || got.ID != "p1" || got.Revision != 2 {
		t.Fatalf("placement annotations = %+v, %v", got, ok)
	}

	err = a.ApplyPlacement(ctx, placement(t, "p1", 1))
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("apply revision 1 after 2: err = %v, want ErrStaleRevision", err)
	}

	err = a.ApplyPlacement(ctx, placement(t, "p1", 2))
	if err != nil {
		t.Fatalf("re-apply revision 2: %v", err)
	}
	err = a.ApplyPlacement(ctx, placement(t, "p1", 3))
	if err != nil {
		t.Fatalf("apply revision 3: %v", err)
	}
	app, err = a.live(t)
	if err != nil {
		t.Fatalf("get Application: %v", err)
	}
	if got, _ := PlacementOf(app); got.Revision != 3 {
		t.Fatalf("revision = %d, want 3", got.Revision)
	}
}

func TestDeletePlacementOnlyDeletesItsOwnApplication(t *testing.T) {
	ctx := context.Background()
	a := newTestApplier(t)

	if err := a.ApplyPlacement(ctx, placement(t, "p1", 1)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	other := placement(t, "p0", 9)
	if err := a.DeletePlacement(ctx, other); err != nil {
		t.Fatalf("delete another placement: %v", err)
	}
	if _, err := a.live(t); err != nil {
		t.Fatalf("Application owned by p1 was deleted for p0: %v", err)
	}

	if err := a.DeletePlacement(ctx, placement(t, "p1", 2)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := a.live(t); !apierrors.IsNotFound(err) {
		t.Fatalf("Application still present after delete, err = %v", err)
	}

	if err := a.DeletePlacement(ctx, placement(t, "p1", 2)); err != nil {
		t.Fatalf("delete missing Application: %v", err)
	}
}

func TestDeletePlacementRejectsStaleRevision(t *testing.T) {
	ctx := context.Background()
	a := newTestApplier(t)

	if err := a.ApplyPlacement(ctx, placement(t, "p1", 5)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	err := a.DeletePlacement(ctx, placement(t, "p1", 3))
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("delete revision 3 after 5: err = %v, want ErrStaleRevision", err)
	}
	if _, getErr := a.live(t); getErr != nil {
		t.Fatalf("a stale delete removed the Application: %v", getErr)
	}
}

func TestInvalidPlacementsAreMarked(t *testing.T) {
	noSpec, err := json.Marshal(DeployPayload{ResourceID: testResourceID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	cases := []struct {
		name      string
		placement Placement
	}{
		{name: "malformed json", placement: Placement{ID: "p", ResourceID: testResourceID, Application: []byte("{")}},
		{name: "missing app_spec", placement: Placement{ID: "p", ResourceID: testResourceID, Application: noSpec}},
		{name: "missing resource_id", placement: Placement{ID: "p", Application: application(t)}},
		{
			name:      "missing placement id",
			placement: Placement{ResourceID: testResourceID, Application: application(t)},
		},
		{
			name: "env secret without a service spec",
			placement: Placement{
				ID:          "p",
				ResourceID:  testResourceID,
				Application: application(t),
				EnvSecret:   &EnvSecret{Revision: 1},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApplier(t)
			applyErr := a.ApplyPlacement(context.Background(), tc.placement)
			if !errors.Is(applyErr, ErrInvalidPayload) {
				t.Fatalf("ApplyPlacement error = %v, want ErrInvalidPayload", applyErr)
			}
		})
	}
}
