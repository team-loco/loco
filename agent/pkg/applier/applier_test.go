package applier

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testNamespace  = "loco-system"
	testResourceID = "abc"
)

func newTestApplier(t *testing.T) *Applier {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	return &Applier{client: c, namespace: testNamespace}
}

func application(t *testing.T, resourceID string) []byte {
	t.Helper()
	payload, err := json.Marshal(DeployPayload{
		ResourceID: resourceID,
		AppSpec:    &locoControllerV1.ApplicationSpec{Type: "SERVICE"},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return payload
}

func placement(t *testing.T, id string, revision int64) Placement {
	t.Helper()
	return Placement{
		ID:          id,
		Revision:    revision,
		ResourceID:  testResourceID,
		Application: application(t, testResourceID),
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
		{name: "missing resource_id", placement: Placement{ID: "p", Application: application(t, testResourceID)}},
		{
			name:      "missing placement id",
			placement: Placement{ResourceID: testResourceID, Application: application(t, testResourceID)},
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
