package appwatch

import (
	"context"
	"strconv"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const testNamespace = "loco-system"

func app(name, placementID, revision, phase string) *locoControllerV1.Application {
	annotations := map[string]string{}
	if placementID != "" {
		annotations[locoControllerV1.AnnotationPlacementID] = placementID
		annotations[locoControllerV1.AnnotationPlacementRevision] = revision
	}
	observed, err := strconv.ParseInt(revision, 10, 64)
	if err != nil {
		observed = 0
	}
	return &locoControllerV1.Application{
		Name: name, Namespace: testNamespace, Annotations: annotations,
		Status: locoControllerV1.ApplicationStatus{Phase: phase, ObservedPlacementRevision: observed},
	}
}

func TestStatusReportsTheRevisionTheControllerObserved(t *testing.T) {
	w := New(nil, testNamespace)
	lagging := app("resource-a", "p1", "5", "Ready")
	lagging.Status.ObservedPlacementRevision = 4

	var got []*agentv1.PlacementStatus
	w.Attach(func(s *agentv1.PlacementStatus) { got = append(got, s) })
	w.Observe(lagging)
	if len(got) != 1 || got[0].GetObservedRevision() != 4 {
		t.Fatalf("statuses = %v, want observed revision 4", got)
	}
}

func TestStatusForwardsReplicaChangesWithoutAPhaseChange(t *testing.T) {
	w := New(nil, testNamespace)
	application := app("resource-a", "p1", "5", "Deploying")
	application.Status.ReadyReplicas = 1
	var got []*agentv1.PlacementStatus
	w.Attach(func(status *agentv1.PlacementStatus) { got = append(got, status) })
	w.Observe(application)
	application.Status.ReadyReplicas = 2
	w.Observe(application)
	w.Observe(application)
	application.Status.ReadyReplicas = 0
	w.Observe(application)
	if len(got) != 3 {
		t.Fatalf("statuses = %v, want three replica changes", got)
	}
	for index, count := range []int32{1, 2, 0} {
		if got[index].GetReadyReplicas() != count || got[index].GetReady() {
			t.Errorf("status = %v, want %d ready replicas while Deploying", got[index], count)
		}
	}
}

func TestInventorySkipsApplicationsWithoutPlacement(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		app("resource-a", "p1", "4", ""),
		app("resource-b", "", "", ""),
	).Build()

	inventory, err := New(c, testNamespace).Inventory(context.Background())
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	entries := inventory.GetEntries()
	if len(entries) != 1 || entries[0].GetPlacementId() != "p1" || entries[0].GetRevision() != 4 {
		t.Fatalf("entries = %v, want p1 at revision 4", entries)
	}
}

func TestStatusIsForwardedOnlyWhenItChanges(t *testing.T) {
	w := New(nil, testNamespace)
	w.Observe(app("resource-a", "p1", "2", "Deploying"))

	var got []*agentv1.PlacementStatus
	detach := w.Attach(func(s *agentv1.PlacementStatus) { got = append(got, s) })
	if len(got) != 1 || got[0].GetPhase() != "Deploying" || got[0].GetObservedRevision() != 2 {
		t.Fatalf("attach replay = %v", got)
	}

	w.Observe(app("resource-a", "p1", "2", "Deploying"))
	w.Observe(app("resource-a", "p1", "2", "Ready"))
	if len(got) != 2 || !got[1].GetReady() {
		t.Fatalf("statuses = %v, want one more, ready", got)
	}

	detach()
	w.Observe(app("resource-a", "p1", "3", "Deploying"))
	if len(got) != 2 {
		t.Fatal("status forwarded after detach")
	}

	deleted := app("resource-a", "p1", "3", "Deploying")
	w.Forget(toolscache.DeletedFinalStateUnknown{Obj: deleted})
	var replay []*agentv1.PlacementStatus
	w.Attach(func(s *agentv1.PlacementStatus) { replay = append(replay, s) })
	if len(replay) != 0 {
		t.Fatalf("deleted Application still replayed: %v", replay)
	}
}
