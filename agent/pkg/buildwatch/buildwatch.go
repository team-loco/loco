package buildwatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	namePrefix        = "build-"
	fieldOwner        = "loco-agent"
	labelManagedBy    = "app.kubernetes.io/managed-by"
	labelBuildID      = "loco.io/build-id"
	managedByValue    = "loco-agent"
	maxMessageLength  = 4000
	truncatedPrefix   = "..."
	createRetryFactor = 2
	createRetryJitter = 0.1
)

var (
	ErrInvalidBuild   = errors.New("invalid build")
	errCacheNotSynced = errors.New("build cache did not sync")
)

type Config struct {
	Namespace           string
	Retention           time.Duration
	CreateRetryDelay    time.Duration
	CreateRetryAttempts int
}

type Sink func(*agentv1.BuildStatus)

type Watcher struct {
	client        client.Client
	reader        client.Reader
	namespace     string
	retention     time.Duration
	createBackoff wait.Backoff
	now           func() time.Time

	order    sync.Mutex
	mu       sync.Mutex
	statuses map[string]*agentv1.BuildStatus
	sink     Sink
	sinkID   uint64
}

func Start(ctx context.Context, restConfig *rest.Config, cfg Config) (*Watcher, error) {
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add loco types to scheme: %w", err)
	}
	c, err := cache.New(restConfig, cache.Options{
		Scheme:            scheme,
		DefaultNamespaces: map[string]cache.Config{cfg.Namespace: {}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Build cache: %w", err)
	}
	writer, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create Build client: %w", err)
	}

	w := New(writer, c, cfg)
	informer, err := c.GetInformer(ctx, &locoControllerV1.Build{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Build informer: %w", err)
	}
	_, err = informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    w.Observe,
		UpdateFunc: w.observeUpdate,
		DeleteFunc: w.Forget,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to watch Builds: %w", err)
	}

	go func() {
		if startErr := c.Start(ctx); startErr != nil {
			slog.ErrorContext(ctx, "Build cache stopped", "error", startErr)
		}
	}()
	if !c.WaitForCacheSync(ctx) {
		return nil, errCacheNotSynced
	}
	return w, nil
}

func New(writer client.Client, reader client.Reader, cfg Config) *Watcher {
	createBackoff := wait.Backoff{
		Duration: cfg.CreateRetryDelay,
		Factor:   createRetryFactor,
		Jitter:   createRetryJitter,
		Steps:    cfg.CreateRetryAttempts,
	}
	return &Watcher{
		client:        writer,
		reader:        reader,
		namespace:     cfg.Namespace,
		retention:     cfg.Retention,
		createBackoff: createBackoff,
		now:           time.Now,
		statuses:      make(map[string]*agentv1.BuildStatus),
	}
}

func Name(buildID string) string {
	return namePrefix + buildID
}

func (w *Watcher) Create(ctx context.Context, start *agentv1.StartBuild) error {
	buildID := start.GetBuildId()
	if buildID == "" {
		return fmt.Errorf("%w: no build id", ErrInvalidBuild)
	}
	name := Name(buildID)
	build := &locoControllerV1.Build{
		Name:      name,
		Namespace: w.namespace,
		Labels: map[string]string{
			labelManagedBy: managedByValue,
			labelBuildID:   buildID,
		},
		Spec: locoControllerV1.BuildSpec{
			BuildID:         buildID,
			WorkspaceID:     start.GetWorkspaceId(),
			ResourceID:      start.GetResourceId(),
			SourceURL:       start.GetSourceUrl(),
			DockerfilePath:  start.GetDockerfilePath(),
			Context:         start.GetContext(),
			ImageRepository: start.GetImageRepository(),
			CacheRef:        start.GetCacheRef(),
		},
	}
	err := w.client.Create(ctx, build, client.FieldOwner(fieldOwner))
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	if apierrors.IsInvalid(err) {
		return fmt.Errorf("%w: %w", ErrInvalidBuild, err)
	}
	if err != nil {
		return fmt.Errorf("create Build %s: %w", name, err)
	}
	return nil
}

func (w *Watcher) Delete(ctx context.Context, buildID string) error {
	name := Name(buildID)
	build := &locoControllerV1.Build{
		Name: name, Namespace: w.namespace,
	}
	propagation := client.PropagationPolicy(metav1.DeletePropagationBackground)
	err := w.client.Delete(ctx, build, propagation)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete Build %s: %w", name, err)
	}
	return nil
}

func (w *Watcher) list(ctx context.Context) ([]locoControllerV1.Build, error) {
	var builds locoControllerV1.BuildList
	if err := w.reader.List(ctx, &builds, client.InNamespace(w.namespace)); err != nil {
		return nil, fmt.Errorf("failed to list Builds: %w", err)
	}
	return builds.Items, nil
}

func (w *Watcher) Inventory(ctx context.Context) ([]*agentv1.InventoryBuild, error) {
	builds, err := w.list(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]*agentv1.InventoryBuild, 0, len(builds))
	for i := range builds {
		build := &builds[i]
		if build.Spec.BuildID == "" || build.Name != Name(build.Spec.BuildID) {
			continue
		}
		phase := Phase(build.Status.Phase)
		if phase == agentv1.BuildPhase_BUILD_PHASE_UNSPECIFIED {
			phase = agentv1.BuildPhase_BUILD_PHASE_PENDING
		}
		entries = append(entries, &agentv1.InventoryBuild{
			BuildId: build.Spec.BuildID,
			Phase:   phase,
		})
	}
	return entries, nil
}

func (w *Watcher) WithInventory(ctx context.Context, fn func([]*agentv1.InventoryBuild) error) error {
	w.order.Lock()
	defer w.order.Unlock()
	entries, err := w.Inventory(ctx)
	if err != nil {
		return err
	}
	return fn(entries)
}

func (w *Watcher) Attach(sink Sink) func() {
	w.order.Lock()
	defer w.order.Unlock()

	w.mu.Lock()
	w.sinkID++
	id := w.sinkID
	w.sink = sink
	snapshot := make([]*agentv1.BuildStatus, 0, len(w.statuses))
	for _, status := range w.statuses {
		snapshot = append(snapshot, status)
	}
	w.mu.Unlock()

	for _, status := range snapshot {
		sink(status)
	}

	return func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.sinkID == id {
			w.sink = nil
		}
	}
}

func Phase(phase string) agentv1.BuildPhase {
	switch phase {
	case locoControllerV1.BuildPhasePending:
		return agentv1.BuildPhase_BUILD_PHASE_PENDING
	case locoControllerV1.BuildPhaseRunning:
		return agentv1.BuildPhase_BUILD_PHASE_RUNNING
	case locoControllerV1.BuildPhaseSucceeded:
		return agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED
	case locoControllerV1.BuildPhaseFailed:
		return agentv1.BuildPhase_BUILD_PHASE_FAILED
	case locoControllerV1.BuildPhaseCanceled:
		return agentv1.BuildPhase_BUILD_PHASE_CANCELED
	default:
		return agentv1.BuildPhase_BUILD_PHASE_UNSPECIFIED
	}
}

func Truncate(message string) string {
	runes := []rune(message)
	if len(runes) <= maxMessageLength {
		return message
	}
	keep := maxMessageLength - len(truncatedPrefix)
	tail := runes[len(runes)-keep:]
	return truncatedPrefix + string(tail)
}

func StatusOf(build *locoControllerV1.Build) (*agentv1.BuildStatus, bool) {
	if build.Spec.BuildID == "" || build.Name != Name(build.Spec.BuildID) {
		return nil, false
	}
	phase := Phase(build.Status.Phase)
	if phase == agentv1.BuildPhase_BUILD_PHASE_UNSPECIFIED {
		return nil, false
	}
	message := Truncate(build.Status.Message)
	status := &agentv1.BuildStatus{
		BuildId: build.Spec.BuildID,
		Phase:   phase,
		Message: message,
	}
	if phase == agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED {
		status.ImageDigest = build.Status.ImageDigest
		status.CacheDigest = build.Status.CacheDigest
	}
	return status, true
}

func (w *Watcher) observeUpdate(_, obj any) {
	w.Observe(obj)
}

func (w *Watcher) Observe(obj any) {
	build, ok := obj.(*locoControllerV1.Build)
	if !ok {
		return
	}
	status, ok := StatusOf(build)
	if !ok {
		return
	}

	w.order.Lock()
	defer w.order.Unlock()

	w.mu.Lock()
	previous := w.statuses[status.GetBuildId()]
	if proto.Equal(previous, status) {
		w.mu.Unlock()
		return
	}
	w.statuses[status.GetBuildId()] = status
	sink := w.sink
	w.mu.Unlock()

	if sink != nil {
		sink(status)
	}
}

func (w *Watcher) Forget(obj any) {
	if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	build, ok := obj.(*locoControllerV1.Build)
	if !ok {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.statuses, build.Spec.BuildID)
}

func (w *Watcher) Collect(ctx context.Context) error {
	builds, err := w.list(ctx)
	if err != nil {
		return err
	}
	cutoff := w.now().Add(-w.retention)
	var errs []error
	for i := range builds {
		build := &builds[i]
		if build.Spec.BuildID == "" || build.Name != Name(build.Spec.BuildID) {
			continue
		}
		if !build.Finished() || build.Status.FinishedAt == nil {
			continue
		}
		if build.Status.FinishedAt.After(cutoff) {
			continue
		}
		if deleteErr := w.Delete(ctx, build.Spec.BuildID); deleteErr != nil {
			errs = append(errs, deleteErr)
			continue
		}
		slog.InfoContext(ctx, "removed finished build", "build_id", build.Spec.BuildID, "phase", build.Status.Phase)
	}
	return errors.Join(errs...)
}

func (w *Watcher) RunCollector(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.Collect(ctx); err != nil {
				slog.WarnContext(ctx, "failed to remove finished builds", "error", err)
			}
		}
	}
}

func retriable(err error) bool {
	return !errors.Is(err, ErrInvalidBuild) && !apierrors.IsForbidden(err)
}

func (w *Watcher) Handle(ctx context.Context, msg *agentv1.SyncResponse, report Sink) {
	switch m := msg.GetMessage().(type) {
	case *agentv1.SyncResponse_StartBuild:
		w.handleStart(ctx, m.StartBuild, report)
	case *agentv1.SyncResponse_CancelBuild:
		buildID := m.CancelBuild.GetBuildId()
		slog.InfoContext(ctx, "received build cancel", "build_id", buildID)
		if err := w.Delete(ctx, buildID); err != nil {
			slog.WarnContext(ctx, "failed to cancel build", "build_id", buildID, "error", err)
		}
	default:
		slog.WarnContext(ctx, "ignoring non-build sync message")
	}
}

func (w *Watcher) handleStart(ctx context.Context, start *agentv1.StartBuild, report Sink) {
	buildID := start.GetBuildId()
	slog.InfoContext(ctx, "received build",
		"build_id", buildID,
		"resource_id", start.GetResourceId(),
		"cache_ref", start.GetCacheRef(),
	)
	err := retry.OnError(w.createBackoff, retriable, func() error {
		return w.Create(ctx, start)
	})
	if err == nil {
		return
	}
	slog.ErrorContext(ctx, "failed to create build", "build_id", buildID, "error", err)
	if ctx.Err() != nil {
		return
	}
	message := "the agent could not create the build: " + err.Error()
	truncated := Truncate(message)
	report(&agentv1.BuildStatus{
		BuildId: buildID,
		Phase:   agentv1.BuildPhase_BUILD_PHASE_FAILED,
		Message: truncated,
	})
}
