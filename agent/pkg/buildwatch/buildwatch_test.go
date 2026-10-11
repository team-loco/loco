package buildwatch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testNamespace           = "loco-builds"
	testBuildID             = "01a11111-1111-7111-8111-111111111111"
	testOtherID             = "01a22222-2222-7222-8222-222222222222"
	testDigest              = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testCreateRetryAttempts = 3
	testCache               = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return scheme
}

func testConfig() Config {
	return Config{
		Namespace:           testNamespace,
		Retention:           time.Hour,
		CreateRetryDelay:    time.Millisecond,
		CreateRetryAttempts: testCreateRetryAttempts,
	}
}

func newWatcher(t *testing.T, objs ...client.Object) (*Watcher, client.Client) {
	t.Helper()
	scheme := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return New(c, c, testConfig()), c
}

func startMessage(buildID string) *agentv1.StartBuild {
	return &agentv1.StartBuild{
		BuildId:         buildID,
		WorkspaceId:     "01a33333-3333-7333-8333-333333333333",
		ResourceId:      "01a44444-4444-7444-8444-444444444444",
		SourceUrl:       "https://bucket.test/sources/x.tar.gz?signed",
		DockerfilePath:  "deploy/Dockerfile",
		Context:         "services/api",
		ImageRepository: "registry.test/ws-1/app",
		CacheRef:        "registry.test/ws-1/app@" + testCache,
	}
}

func build(name, buildID, phase string) *locoControllerV1.Build {
	return &locoControllerV1.Build{
		Name: name, Namespace: testNamespace,
		Spec:   locoControllerV1.BuildSpec{BuildID: buildID},
		Status: locoControllerV1.BuildStatus{Phase: phase},
	}
}

func getBuild(t *testing.T, c client.Client, buildID string) (*locoControllerV1.Build, error) {
	t.Helper()
	var got locoControllerV1.Build
	key := client.ObjectKey{Namespace: testNamespace, Name: Name(buildID)}
	err := c.Get(context.Background(), key, &got)
	return &got, err
}

func TestCreateMakesOneBuildPerID(t *testing.T) {
	w, c := newWatcher(t)
	ctx := context.Background()
	start := startMessage(testBuildID)

	if err := w.Create(ctx, start); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := w.Create(ctx, start); err != nil {
		t.Fatalf("second create: %v", err)
	}

	got, err := getBuild(t, c, testBuildID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := locoControllerV1.BuildSpec{
		BuildID:         testBuildID,
		WorkspaceID:     start.GetWorkspaceId(),
		ResourceID:      start.GetResourceId(),
		SourceURL:       start.GetSourceUrl(),
		DockerfilePath:  start.GetDockerfilePath(),
		Context:         start.GetContext(),
		ImageRepository: start.GetImageRepository(),
		CacheRef:        start.GetCacheRef(),
	}
	if got.Spec != want {
		t.Fatalf("spec = %+v, want %+v", got.Spec, want)
	}
	if got.Labels[labelBuildID] != testBuildID {
		t.Fatalf("labels = %v", got.Labels)
	}

	var list locoControllerV1.BuildList
	if err := c.List(ctx, &list); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("%d Builds, want 1", len(list.Items))
	}
}

func TestCancelDeletesTheBuildAndToleratesAMissingOne(t *testing.T) {
	w, c := newWatcher(t, build(Name(testBuildID), testBuildID, locoControllerV1.BuildPhaseRunning))
	ctx := context.Background()

	var reported []*agentv1.BuildStatus
	report := func(s *agentv1.BuildStatus) { reported = append(reported, s) }
	cancel := &agentv1.SyncResponse{Message: &agentv1.SyncResponse_CancelBuild{
		CancelBuild: &agentv1.CancelBuild{BuildId: testBuildID},
	}}
	w.Handle(ctx, cancel, report)
	if _, err := getBuild(t, c, testBuildID); !apierrors.IsNotFound(err) {
		t.Fatalf("get after cancel: %v, want not found", err)
	}
	w.Handle(ctx, cancel, report)
	if len(reported) != 0 {
		t.Fatalf("reported %v for a cancel", reported)
	}
}

func TestStartThatTheAPIServerRejectsReportsAFailure(t *testing.T) {
	scheme := newScheme(t)
	creates := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			creates++
			gk := schema.GroupKind{Group: "infra.loco.io", Kind: "Build"}
			invalid := field.Invalid(field.NewPath("spec", "sourceURL"), "ftp://x", "must be http")
			return apierrors.NewInvalid(gk, Name(testBuildID), field.ErrorList{invalid})
		},
	}).Build()
	w := New(c, c, testConfig())

	var reported []*agentv1.BuildStatus
	start := &agentv1.SyncResponse{Message: &agentv1.SyncResponse_StartBuild{StartBuild: startMessage(testBuildID)}}
	w.Handle(context.Background(), start, func(s *agentv1.BuildStatus) { reported = append(reported, s) })

	if creates != 1 {
		t.Fatalf("%d create attempts for an invalid Build, want 1", creates)
	}
	if len(reported) != 1 {
		t.Fatalf("reported %v, want one failure", reported)
	}
	got := reported[0]
	if got.GetBuildId() != testBuildID || got.GetPhase() != agentv1.BuildPhase_BUILD_PHASE_FAILED {
		t.Fatalf("reported %v, want a failure for %s", got, testBuildID)
	}
	if !strings.Contains(got.GetMessage(), "sourceURL") {
		t.Fatalf("message %q does not explain the rejection", got.GetMessage())
	}
}

func TestCreateRetriesUpToTheConfiguredAttempts(t *testing.T) {
	scheme := newScheme(t)
	creates := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			creates++
			return apierrors.NewServiceUnavailable("down")
		},
	}).Build()
	w := New(c, c, testConfig())

	var reported []*agentv1.BuildStatus
	start := &agentv1.SyncResponse{Message: &agentv1.SyncResponse_StartBuild{StartBuild: startMessage(testBuildID)}}
	w.Handle(context.Background(), start, func(s *agentv1.BuildStatus) { reported = append(reported, s) })

	if creates != testCreateRetryAttempts {
		t.Fatalf("%d create attempts, want %d", creates, testCreateRetryAttempts)
	}
	if len(reported) != 1 || reported[0].GetPhase() != agentv1.BuildPhase_BUILD_PHASE_FAILED {
		t.Fatalf("reported %v, want one failure", reported)
	}
}

func TestRetriable(t *testing.T) {
	gr := schema.GroupResource{Group: "infra.loco.io", Resource: "builds"}
	invalid := errors.Join(ErrInvalidBuild, errors.New("bad"))
	forbidden := apierrors.NewForbidden(gr, "build-x", errors.New("rbac"))
	unavailable := apierrors.NewServiceUnavailable("down")
	if retriable(invalid) || retriable(forbidden) {
		t.Fatal("a permanent error was retried")
	}
	if !retriable(unavailable) {
		t.Fatal("a transient error was not retried")
	}
}

func TestInventoryListsAgentBuildsWithTheirPhase(t *testing.T) {
	w, _ := newWatcher(t,
		build(Name(testBuildID), testBuildID, ""),
		build(Name(testOtherID), testOtherID, locoControllerV1.BuildPhaseSucceeded),
		build("e2e-app-1", "e2e-app-1", locoControllerV1.BuildPhaseRunning),
	)

	var got []*agentv1.InventoryBuild
	err := w.WithInventory(context.Background(), func(entries []*agentv1.InventoryBuild) error {
		got = entries
		return nil
	})
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	phases := map[string]agentv1.BuildPhase{}
	for _, entry := range got {
		phases[entry.GetBuildId()] = entry.GetPhase()
	}
	want := map[string]agentv1.BuildPhase{
		testBuildID: agentv1.BuildPhase_BUILD_PHASE_PENDING,
		testOtherID: agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED,
	}
	if len(phases) != len(want) {
		t.Fatalf("inventory = %v, want %v", phases, want)
	}
	for id, phase := range want {
		if phases[id] != phase {
			t.Fatalf("inventory = %v, want %v", phases, want)
		}
	}
}

func TestStatusIsForwardedWhenItChanges(t *testing.T) {
	w, _ := newWatcher(t)
	w.Observe(build(Name(testBuildID), testBuildID, ""))
	w.Observe(build(Name(testBuildID), testBuildID, locoControllerV1.BuildPhasePending))

	var got []*agentv1.BuildStatus
	detach := w.Attach(func(s *agentv1.BuildStatus) { got = append(got, s) })
	if len(got) != 1 || got[0].GetPhase() != agentv1.BuildPhase_BUILD_PHASE_PENDING {
		t.Fatalf("attach replay = %v", got)
	}

	w.Observe(build(Name(testBuildID), testBuildID, locoControllerV1.BuildPhasePending))
	running := build(Name(testBuildID), testBuildID, locoControllerV1.BuildPhaseRunning)
	running.Status.ImageDigest = testDigest
	w.Observe(running)
	if len(got) != 2 || got[1].GetPhase() != agentv1.BuildPhase_BUILD_PHASE_RUNNING {
		t.Fatalf("statuses = %v, want one more, running", got)
	}
	if got[1].GetImageDigest() != "" {
		t.Fatal("a digest was forwarded before the build succeeded")
	}

	succeeded := build(Name(testBuildID), testBuildID, locoControllerV1.BuildPhaseSucceeded)
	succeeded.Status.ImageDigest = testDigest
	succeeded.Status.CacheDigest = testCache
	w.Observe(succeeded)
	if len(got) != 3 || got[2].GetImageDigest() != testDigest || got[2].GetCacheDigest() != testCache {
		t.Fatalf("statuses = %v, want a success with both digests", got)
	}

	w.Observe(build("e2e-app-1", "e2e-app-1", locoControllerV1.BuildPhaseRunning))
	if len(got) != 3 {
		t.Fatal("status forwarded for a Build the agent did not create")
	}

	detach()
	w.Observe(build(Name(testBuildID), testBuildID, locoControllerV1.BuildPhaseFailed))
	if len(got) != 3 {
		t.Fatal("status forwarded after detach")
	}

	w.Forget(toolscache.DeletedFinalStateUnknown{Obj: build(Name(testBuildID), testBuildID, "")})
	var replay []*agentv1.BuildStatus
	w.Attach(func(s *agentv1.BuildStatus) { replay = append(replay, s) })
	if len(replay) != 0 {
		t.Fatalf("deleted Build still replayed: %v", replay)
	}
}

func TestCollectRemovesOnlyOldFinishedBuilds(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	old := metav1.NewTime(now.Add(-2 * time.Hour))
	recent := metav1.NewTime(now.Add(-10 * time.Minute))

	oldDone := build(Name(testBuildID), testBuildID, locoControllerV1.BuildPhaseSucceeded)
	oldDone.Status.FinishedAt = &old
	recentDone := build(Name(testOtherID), testOtherID, locoControllerV1.BuildPhaseFailed)
	recentDone.Status.FinishedAt = &recent
	runningID := "01a55555-5555-7555-8555-555555555555"
	running := build(Name(runningID), runningID, locoControllerV1.BuildPhaseRunning)
	foreign := build("e2e-app-1", "e2e-app-1", locoControllerV1.BuildPhaseSucceeded)
	foreign.Status.FinishedAt = &old

	w, c := newWatcher(t, oldDone, recentDone, running, foreign)
	w.now = func() time.Time { return now }
	if err := w.Collect(context.Background()); err != nil {
		t.Fatalf("collect: %v", err)
	}

	if _, err := getBuild(t, c, testBuildID); !apierrors.IsNotFound(err) {
		t.Fatalf("old finished build: %v, want deleted", err)
	}
	for _, id := range []string{testOtherID, runningID} {
		if _, err := getBuild(t, c, id); err != nil {
			t.Fatalf("build %s: %v, want kept", id, err)
		}
	}
	var kept locoControllerV1.Build
	key := client.ObjectKey{Namespace: testNamespace, Name: "e2e-app-1"}
	if err := c.Get(context.Background(), key, &kept); err != nil {
		t.Fatalf("foreign build: %v, want kept", err)
	}
}

func TestTruncateKeepsTheEnd(t *testing.T) {
	short := "exit 1"
	if got := Truncate(short); got != short {
		t.Fatalf("Truncate(%q) = %q", short, got)
	}
	long := strings.Repeat("a", maxMessageLength) + "tail"
	got := Truncate(long)
	if len([]rune(got)) != maxMessageLength || !strings.HasSuffix(got, "tail") ||
		!strings.HasPrefix(got, truncatedPrefix) {
		t.Fatalf("Truncate kept %d runes, prefix %q", len([]rune(got)), got[:5])
	}
}
