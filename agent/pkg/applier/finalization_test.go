package applier

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestPlacementDeletionWaitsForApplicationFinalization(t *testing.T) {
	app := appliedApplication(1, true)
	app.Finalizers = []string{loco.FinalizerAppResourcesCleanup}
	a := newTestApplier(t, app)
	deletion := placement(t, testPlacementID, 2)
	if err := a.DeletePlacement(t.Context(), deletion); !apierrors.IsConflict(err) {
		t.Fatalf("delete before finalization = %v, want retryable conflict", err)
	}
	retiring, err := a.live(t)
	if err != nil {
		t.Fatal(err)
	}
	if retiring.DeletionTimestamp.IsZero() {
		t.Fatal("Application deletion was not requested")
	}
	retiring.Finalizers = nil
	if err := a.client.Update(t.Context(), retiring); err != nil {
		t.Fatal(err)
	}
	if err := a.DeletePlacement(t.Context(), deletion); err != nil {
		t.Fatalf("delete after finalization = %v", err)
	}
	// Only the acknowledged retirement lets the API allocate another placement ID.
	replacement := placement(t, "replacement", 1)
	if err := a.ApplyPlacement(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if err := a.DeletePlacement(t.Context(), deletion); err != nil {
		t.Fatal(err)
	}
	current := &loco.Application{}
	key := client.ObjectKey{Namespace: testNamespace, Name: loco.ApplicationName(replacement.ID)}
	if err := a.client.Get(t.Context(), key, current); err != nil {
		t.Fatalf("late deletion removed the replacement: %v", err)
	}
}

func TestReenabledPlacementWaitsBeforeUpdatingRetiringApplicationSecrets(t *testing.T) {
	app := appliedApplication(1, true)
	app.Finalizers = []string{loco.FinalizerAppResourcesCleanup}
	app.DeletionTimestamp = new(metav1.Now())
	a, writes := newRecordingApplier(t, app, stagedSecret(testPlacementID, 1, "old"))
	requested := placement(t, testPlacementID, 3)
	requested.Application = serviceApplication(t)
	requested.EnvSecret = &EnvSecret{Revision: 3, Data: map[string][]byte{databaseURLName: []byte("new")}}
	if err := a.ApplyPlacement(t.Context(), requested); !apierrors.IsConflict(err) {
		t.Fatalf("apply during finalization = %v, want retryable conflict", err)
	}
	if len(writes.writes) != 0 {
		t.Fatalf("apply during finalization wrote %v", writes.writes)
	}
	secret, err := a.envSecret(t)
	if err != nil || string(secret.Data[databaseURLName]) != "old" {
		t.Fatalf("retiring placement's staged Secret changed: %v", err)
	}
	app.Finalizers = nil
	if err = a.client.Update(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	// Real Kubernetes garbage-collects the staged Secret with its Application.
	if err = a.client.Delete(t.Context(), &corev1.Secret{Name: secret.Name, Namespace: secret.Namespace}); err != nil {
		t.Fatal(err)
	}
	if err = a.ApplyPlacement(t.Context(), requested); err != nil {
		t.Fatalf("apply after finalization = %v", err)
	}
	secret, err = a.envSecret(t)
	if err != nil || string(secret.Data[databaseURLName]) != "new" {
		t.Fatalf("reenabled placement's staged Secret missing: %v", err)
	}
	if err := a.DeletePlacement(t.Context(), placement(t, testPlacementID, 2)); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("late old delete = %v, want stale revision", err)
	}
}
