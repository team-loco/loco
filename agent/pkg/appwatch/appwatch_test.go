package appwatch

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/team-loco/loco/agent/pkg/applier"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testNamespace   = "loco-system"
	testPlacementID = "p1"
	testResourceID  = "abc"
	testPort        = 8080
)

var errAgentCrashed = errors.New("agent stopped between the Application and the Secret write")

func app(name, placementID, revision string) *locoControllerV1.Application {
	annotations := map[string]string{}
	if placementID != "" {
		annotations[locoControllerV1.KeyPlacementID] = placementID
		annotations[locoControllerV1.KeyPlacementRevision] = revision
	}
	observed, err := strconv.ParseInt(revision, 10, 64)
	if err != nil {
		observed = 0
	}
	return &locoControllerV1.Application{
		Name: name, Namespace: testNamespace, Annotations: annotations,
		Status: locoControllerV1.ApplicationStatus{ObservedPlacementRevision: observed},
	}
}

func TestStatusReportsTheRevisionTheControllerObserved(t *testing.T) {
	w := newWatcher(nil, nil, testNamespace)
	lagging := app("resource-a", "p1", "5")
	lagging.Status.ObservedPlacementRevision = 4

	var got []*agentv1.PlacementStatus
	w.Attach(func(s *agentv1.PlacementStatus) { got = append(got, s) })
	w.observe(lagging)
	if len(got) != 1 || got[0].GetObservedRevision() != 4 {
		t.Fatalf("statuses = %v, want observed revision 4", got)
	}
}

func TestStatusForwardsReadyReplicaChanges(t *testing.T) {
	w := newWatcher(nil, nil, testNamespace)
	application := app("resource-a", "p1", "5")
	application.Status.ReadyReplicas = 1
	var got []*agentv1.PlacementStatus
	w.Attach(func(status *agentv1.PlacementStatus) { got = append(got, status) })
	w.observe(application)
	application.Status.ReadyReplicas = 2
	w.observe(application)
	w.observe(application)
	application.Status.ReadyReplicas = 0
	w.observe(application)
	if len(got) != 3 {
		t.Fatalf("statuses = %v, want three replica changes", got)
	}
	for index, count := range []int32{1, 2, 0} {
		if got[index].GetReadyReplicas() != count {
			t.Errorf("status = %v, want %d ready replicas", got[index], count)
		}
	}
}

func TestInventorySkipsApplicationsWithoutPlacement(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		app("resource-a", "p1", "4"),
		app("resource-b", "", ""),
	).Build()

	inventory, err := newWatcher(c, c, testNamespace).inventory(context.Background())
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	entries := inventory.GetEntries()
	if len(entries) != 1 || entries[0].GetPlacementId() != "p1" || entries[0].GetRevision() != 4 {
		t.Fatalf("entries = %v, want p1 at revision 4", entries)
	}
}

func TestStatusIsForwardedOnlyWhenItChanges(t *testing.T) {
	w := newWatcher(nil, nil, testNamespace)
	w.observe(app("resource-a", "p1", "2"))

	var got []*agentv1.PlacementStatus
	detach := w.Attach(func(s *agentv1.PlacementStatus) { got = append(got, s) })
	if len(got) != 1 || got[0].GetAvailable() || got[0].GetObservedRevision() != 2 {
		t.Fatalf("attach replay = %v", got)
	}

	w.observe(app("resource-a", "p1", "2"))
	available := app("resource-a", "p1", "2")
	available.Status.Rollout = &locoControllerV1.RolloutStatus{Available: true}
	w.observe(available)
	if len(got) != 2 || !got[1].GetAvailable() {
		t.Fatalf("statuses = %v, want one more, ready", got)
	}

	detach()
	w.observe(app("resource-a", "p1", "3"))
	if len(got) != 2 {
		t.Fatal("status forwarded after detach")
	}

	deleted := app("resource-a", "p1", "3")
	w.forget(deleted)
	var replay []*agentv1.PlacementStatus
	w.Attach(func(s *agentv1.PlacementStatus) { replay = append(replay, s) })
	if len(replay) != 0 {
		t.Fatalf("deleted Application still replayed: %v", replay)
	}
}

func envSecretPlacement(t *testing.T, revision int64) applier.Placement {
	t.Helper()
	spec := &locoControllerV1.ApplicationSpec{
		Type:       "SERVICE",
		ResourceID: testResourceID,
		ServiceSpec: &locoControllerV1.ServiceSpec{
			Deployment: &locoControllerV1.ServiceDeploymentSpec{
				Image: "ghcr.io/team-loco/app:v1",
				Port:  new(int32(testPort)),
			},
		},
	}
	payload, err := json.Marshal(locoControllerV1.PlacementPayload{AppSpec: spec})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return applier.Placement{
		ID:          testPlacementID,
		Revision:    revision,
		ResourceID:  testResourceID,
		Application: payload,
		EnvSecret: &applier.EnvSecret{
			Revision: revision,
			Data:     map[string][]byte{"DATABASE_URL": []byte("postgres://db")},
		},
	}
}

type crashBeforeSecretWrite struct {
	crashed bool
}

func (c *crashBeforeSecretWrite) funcs() interceptor.Funcs {
	return interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, isSecret := obj.(*corev1.Secret); isSecret && !c.crashed {
				c.crashed = true
				return errAgentCrashed
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
}

func newCrashingCluster(t *testing.T) (*applier.Applier, *Watcher) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	crash := &crashBeforeSecretWrite{}
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(crash.funcs()).Build()
	return applier.NewWithClient(c, testNamespace, nil), newWatcher(c, c, testNamespace)
}

func singleEntry(t *testing.T, watcher *Watcher) *agentv1.InventoryEntry {
	t.Helper()
	inventory, err := watcher.inventory(context.Background())
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	entries := inventory.GetEntries()
	if len(entries) != 1 || entries[0].GetPlacementId() != testPlacementID {
		t.Fatalf("entries = %v, want one for %s", entries, testPlacementID)
	}
	return entries[0]
}

func TestInventoryMarksAPlacementPendingUntilItsEnvSecretIsWritten(t *testing.T) {
	ctx := context.Background()
	kube, watcher := newCrashingCluster(t)

	err := kube.ApplyPlacement(ctx, envSecretPlacement(t, 1))
	if !errors.Is(err, errAgentCrashed) {
		t.Fatalf("apply = %v, want the crash between the Application and the Secret write", err)
	}
	if entry := singleEntry(t, watcher); entry.GetRevision() != 1 || !entry.GetEnvSecretPending() {
		t.Fatalf("entry = %v, want revision 1 with the env Secret pending", entry)
	}

	if applyErr := kube.ApplyPlacement(ctx, envSecretPlacement(t, 1)); applyErr != nil {
		t.Fatalf("resent apply: %v", applyErr)
	}
	if entry := singleEntry(t, watcher); entry.GetRevision() != 1 || entry.GetEnvSecretPending() {
		t.Fatalf("entry = %v, want revision 1 complete", entry)
	}
}

func TestAPendingPlacementAheadOfARestoredDatabaseConverges(t *testing.T) {
	ctx := context.Background()
	kube, watcher := newCrashingCluster(t)

	err := kube.ApplyPlacement(ctx, envSecretPlacement(t, 5))
	if !errors.Is(err, errAgentCrashed) {
		t.Fatalf("apply = %v, want the crash between the Application and the Secret write", err)
	}
	entry := singleEntry(t, watcher)
	if entry.GetRevision() != 5 || !entry.GetEnvSecretPending() {
		t.Fatalf("entry = %v, want revision 5 with the env Secret pending, so the API can advance past it", entry)
	}

	restored := kube.ApplyPlacement(ctx, envSecretPlacement(t, 1))
	if !errors.Is(restored, applier.ErrStaleRevision) {
		t.Fatalf("apply at the restored revision = %v, want ErrStaleRevision", restored)
	}

	advanced := entry.GetRevision() + 1
	if applyErr := kube.ApplyPlacement(ctx, envSecretPlacement(t, advanced)); applyErr != nil {
		t.Fatalf("apply past the cluster revision: %v", applyErr)
	}
	if entry = singleEntry(t, watcher); entry.GetRevision() != advanced || entry.GetEnvSecretPending() {
		t.Fatalf("entry = %v, want revision %d complete", entry, advanced)
	}
}

func TestInventoryMarksAPlacementPendingWhileItsEnvSecretHasAnotherRevision(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	referencing := app("resource-a", testPlacementID, "3")
	ref := &locoControllerV1.EnvSecretRef{Name: locoControllerV1.EnvSecretName(testPlacementID), Revision: 3}
	deployment := &locoControllerV1.ServiceDeploymentSpec{EnvSecretRef: ref}
	referencing.Spec.ServiceSpec = &locoControllerV1.ServiceSpec{Deployment: deployment}
	staged := &corev1.Secret{
		Name:      locoControllerV1.EnvSecretName(testPlacementID),
		Namespace: testNamespace,
		Labels: map[string]string{
			locoControllerV1.KeyPlacementID:       testPlacementID,
			locoControllerV1.KeyPlacementRevision: "4",
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(referencing, staged).Build()

	entry := singleEntry(t, newWatcher(c, c, testNamespace))
	if entry.GetRevision() != 3 || !entry.GetEnvSecretPending() {
		t.Fatalf("entry = %v, want revision 3 with the env Secret pending", entry)
	}
}

func TestRolloutForwarding(t *testing.T) {
	const currentReplicaName = "new"
	w := newWatcher(nil, nil, testNamespace)
	application := app("rollout", testPlacementID, "5")
	at := metav1.NewTime(time.Date(2026, 11, 16, 12, 0, 0, 123, time.UTC))
	application.Status.Rollout = &locoControllerV1.RolloutStatus{
		DesiredReplicas:          2,
		Available:                true,
		ProgressDeadlineExceeded: true,
		Replicas: []locoControllerV1.ReplicaStatus{
			{
				UID:               "current",
				Name:              currentReplicaName,
				Node:              "node-a",
				PlacementRevision: 5,
				State:             locoControllerV1.ReplicaServing,
				StateSince:        &at,
				Restarts:          2,
				ExitCode:          new(int32(0)),
				Detail:            "ready",
			},
			{
				UID:               "previous",
				Name:              "old",
				PlacementRevision: 4,
				State:             locoControllerV1.ReplicaStopping,
				StateDeadline:     &at,
			},
		},
		Problem: &locoControllerV1.Problem{
			Reason:  locoControllerV1.ProblemCrashLoop,
			Replica: currentReplicaName,
			Detail:  "exit 1",
			Since:   &at,
		},
	}
	var got []*agentv1.PlacementStatus
	w.Attach(func(status *agentv1.PlacementStatus) { got = append(got, status) })
	w.observe(application)
	want := &agentv1.PlacementStatus{
		PlacementId:              testPlacementID,
		ObservedRevision:         5,
		DesiredReplicas:          2,
		Available:                true,
		ProgressDeadlineExceeded: true,
		Replicas: []*agentv1.ReplicaReport{
			{
				Uid:               "current",
				Name:              currentReplicaName,
				Node:              "node-a",
				PlacementRevision: 5,
				State:             "Serving",
				StateSince:        timestamppb.New(at.Time),
				Restarts:          2,
				ExitCode:          new(int32(0)),
				Detail:            "ready",
			},
			{
				Uid:               "previous",
				Name:              "old",
				PlacementRevision: 4,
				State:             "Stopping",
				StateDeadline:     timestamppb.New(at.Time),
			},
		},
		Problem: &agentv1.ProblemReport{
			Reason:      "crash_loop",
			ReplicaName: currentReplicaName,
			Detail:      "exit 1",
			Since:       timestamppb.New(at.Time),
		},
	}
	if len(got) != 1 || !proto.Equal(got[0], want) {
		t.Fatalf("reports=%v want %v", got, want)
	}
	w.observe(application)
	application.Status.Rollout.Replicas[0].State = locoControllerV1.ReplicaFailing
	w.observe(application)
	if len(got) != 2 || got[1].GetReplicas()[0].GetState() != "Failing" {
		t.Fatalf("replica state update=%v", got)
	}
	application.Status.Rollout = nil
	w.observe(application)
	want = &agentv1.PlacementStatus{
		PlacementId:      testPlacementID,
		ObservedRevision: 5,
	}
	if len(got) != 3 || !proto.Equal(got[2], want) {
		t.Fatalf("nil rollout=%v", got)
	}
}
