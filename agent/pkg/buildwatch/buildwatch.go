package buildwatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/agent/pkg/kube"
	"github.com/team-loco/loco/agent/pkg/registrycred"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	namePrefix        = "build-"
	maxMessageLength  = 4000
	truncatedPrefix   = "..."
	createRetryFactor = 2
	createRetryJitter = 0.1
	repositorySep     = "/"
)

var (
	errInvalidBuild    = errors.New("invalid build")
	errNotBuildMessage = errors.New("build handler received a sync message that is not a build operation")
	errNoCredential    = errors.New("no registry credential in token mode")
	errNoRegistryHost  = errors.New("image repository names no registry host")
)

type Config struct {
	Namespace           string
	Retention           time.Duration
	CreateRetryDelay    time.Duration
	CreateRetryAttempts int
	TokenMode           bool
}

type Sink func(*agentv1.BuildStatus)

type Watcher struct {
	client        client.Client
	reader        client.Reader
	namespace     string
	retention     time.Duration
	tokenMode     bool
	createBackoff wait.Backoff
	now           func() time.Time

	feed  *kube.Feed[*agentv1.BuildStatus]
	cache *kube.Cache
}

func Start(ctx context.Context, restConfig *rest.Config, cfg Config) (*Watcher, error) {
	scheme, err := kube.NewScheme()
	if err != nil {
		return nil, err
	}
	writer, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create Build client: %w", err)
	}
	w := newWatcher(writer, nil, cfg)
	c, err := kube.StartCache(ctx, restConfig, scheme, cfg.Namespace, &locoControllerV1.Build{}, w.observe, w.forget)
	if err != nil {
		return nil, err
	}
	// observe and forget never read reader, so it can be set after the cache starts.
	w.reader, w.cache = c, c
	return w, nil
}

// Wait blocks until ctx ends or the Build watch stops for good, and returns why.
func (w *Watcher) Wait(ctx context.Context) error {
	return w.cache.Wait(ctx)
}

func newWatcher(writer client.Client, reader client.Reader, cfg Config) *Watcher {
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
		tokenMode:     cfg.TokenMode,
		createBackoff: createBackoff,
		now:           time.Now,
		feed:          kube.NewFeed[*agentv1.BuildStatus](),
	}
}

func buildName(buildID string) string {
	return namePrefix + buildID
}

func (w *Watcher) create(ctx context.Context, start *agentv1.StartBuild) error {
	buildID := start.GetBuildId()
	if buildID == "" {
		return fmt.Errorf("%w: no build id", errInvalidBuild)
	}
	if w.tokenMode {
		if err := w.writeRegistrySecret(ctx, start); err != nil {
			return err
		}
	}
	name := buildName(buildID)
	build := &locoControllerV1.Build{
		Name:      name,
		Namespace: w.namespace,
		Labels: map[string]string{
			locolabels.LabelManagedBy: locolabels.ManagedByAgent,
			locolabels.LabelBuildID:   buildID,
		},
		Spec: locoControllerV1.BuildSpec{
			BuildID:         buildID,
			OrganizationID:  start.GetOrganizationId(),
			WorkspaceID:     start.GetWorkspaceId(),
			ResourceID:      start.GetResourceId(),
			SourceURL:       start.GetSourceUrl(),
			DockerfilePath:  start.GetDockerfilePath(),
			Context:         start.GetContext(),
			ImageRepository: start.GetImageRepository(),
			CacheRef:        start.GetCacheRef(),
		},
	}
	err := w.client.Create(ctx, build, client.FieldOwner(locolabels.ManagedByAgent))
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	if apierrors.IsInvalid(err) {
		return fmt.Errorf("%w: %w", errInvalidBuild, err)
	}
	if err != nil {
		return fmt.Errorf("create Build %s: %w", name, err)
	}
	return nil
}

func (w *Watcher) writeRegistrySecret(ctx context.Context, start *agentv1.StartBuild) error {
	credential := start.GetRegistryCredential()
	if credential == "" {
		return fmt.Errorf("%w: %w", errInvalidBuild, errNoCredential)
	}
	host, _, ok := strings.Cut(start.GetImageRepository(), repositorySep)
	if !ok || host == "" {
		return fmt.Errorf("%w: %w: %q", errInvalidBuild, errNoRegistryHost, start.GetImageRepository())
	}
	config, err := registrycred.DockerConfig(host, locoControllerV1.RegistryBuildUsername, credential)
	if err != nil {
		return err
	}
	name := locoControllerV1.BuildRegistrySecretName(start.GetBuildId())
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: w.namespace, Name: name}
	getErr := w.client.Get(ctx, key, secret)
	if client.IgnoreNotFound(getErr) != nil {
		return fmt.Errorf("read build registry Secret %s: %w", name, getErr)
	}
	secret.Name = name
	secret.Namespace = w.namespace
	secret.Labels = map[string]string{
		locolabels.LabelManagedBy: locolabels.ManagedByAgent,
		locolabels.LabelBuildID:   start.GetBuildId(),
	}
	secret.Type = corev1.SecretTypeDockerConfigJson
	secret.Data = map[string][]byte{corev1.DockerConfigJsonKey: config}
	if apierrors.IsNotFound(getErr) {
		if err := w.client.Create(ctx, secret); err != nil {
			return fmt.Errorf("create build registry Secret %s: %w", name, err)
		}
		return nil
	}
	if err := w.client.Update(ctx, secret); err != nil {
		return fmt.Errorf("update build registry Secret %s: %w", name, err)
	}
	return nil
}

func (w *Watcher) deleteRegistrySecret(ctx context.Context, buildID string) error {
	name := locoControllerV1.BuildRegistrySecretName(buildID)
	secret := &corev1.Secret{Name: name, Namespace: w.namespace}
	err := w.client.Delete(ctx, secret)
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("delete build registry Secret %s: %w", name, err)
	}
	return nil
}

func (w *Watcher) remove(ctx context.Context, buildID string) error {
	if w.tokenMode {
		if err := w.deleteRegistrySecret(ctx, buildID); err != nil {
			return err
		}
	}
	name := buildName(buildID)
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

func (w *Watcher) inventory(ctx context.Context) ([]*agentv1.InventoryBuild, error) {
	builds, err := w.list(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]*agentv1.InventoryBuild, 0, len(builds))
	for i := range builds {
		build := &builds[i]
		if build.Spec.BuildID == "" || build.Name != buildName(build.Spec.BuildID) {
			continue
		}
		phase := apiPhase(build.Status.Phase)
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

// WithInventory calls fn with the inventory while no status is forwarded, so the API never
// receives an inventory older than a status it already has.
func (w *Watcher) WithInventory(ctx context.Context, fn func([]*agentv1.InventoryBuild) error) error {
	return w.feed.Freeze(func() error {
		entries, err := w.inventory(ctx)
		if err != nil {
			return err
		}
		return fn(entries)
	})
}

// Attach replays every known build status to sink and forwards later changes to it.
func (w *Watcher) Attach(sink Sink) func() {
	return w.feed.Attach(sink)
}

func apiPhase(phase string) agentv1.BuildPhase {
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

func truncate(message string) string {
	runes := []rune(message)
	if len(runes) <= maxMessageLength {
		return message
	}
	keep := maxMessageLength - len(truncatedPrefix)
	tail := runes[len(runes)-keep:]
	return truncatedPrefix + string(tail)
}

func statusOf(build *locoControllerV1.Build) (*agentv1.BuildStatus, bool) {
	if build.Spec.BuildID == "" || build.Name != buildName(build.Spec.BuildID) {
		return nil, false
	}
	phase := apiPhase(build.Status.Phase)
	if phase == agentv1.BuildPhase_BUILD_PHASE_UNSPECIFIED {
		return nil, false
	}
	message := truncate(build.Status.Message)
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

func (w *Watcher) observe(build *locoControllerV1.Build) {
	status, ok := statusOf(build)
	if !ok {
		return
	}
	w.feed.Publish(status.GetBuildId(), status)
}

func (w *Watcher) forget(build *locoControllerV1.Build) {
	w.feed.Forget(build.Spec.BuildID)
}

func (w *Watcher) collect(ctx context.Context) error {
	builds, err := w.list(ctx)
	if err != nil {
		return err
	}
	cutoff := w.now().Add(-w.retention)
	var errs []error
	for i := range builds {
		build := &builds[i]
		if build.Spec.BuildID == "" || build.Name != buildName(build.Spec.BuildID) {
			continue
		}
		if !build.Finished() || build.Status.FinishedAt == nil {
			continue
		}
		if build.Status.FinishedAt.After(cutoff) {
			continue
		}
		if deleteErr := w.remove(ctx, build.Spec.BuildID); deleteErr != nil {
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
			if err := w.collect(ctx); err != nil {
				slog.WarnContext(ctx, "failed to remove finished builds", "error", err)
			}
		}
	}
}

func retriable(err error) bool {
	return !errors.Is(err, errInvalidBuild) && !apierrors.IsForbidden(err)
}

func (w *Watcher) Handle(ctx context.Context, msg *agentv1.SyncResponse, report Sink) {
	switch m := msg.GetMessage().(type) {
	case *agentv1.SyncResponse_StartBuild:
		w.handleStart(ctx, m.StartBuild, report)
	case *agentv1.SyncResponse_CancelBuild:
		buildID := m.CancelBuild.GetBuildId()
		slog.InfoContext(ctx, "received build cancel", "build_id", buildID)
		if err := w.remove(ctx, buildID); err != nil {
			slog.WarnContext(ctx, "failed to cancel build", "build_id", buildID, "error", err)
		}
	default:
		panic(fmt.Errorf("%w: %T", errNotBuildMessage, m))
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
		return w.create(ctx, start)
	})
	if err == nil {
		return
	}
	slog.ErrorContext(ctx, "failed to create build", "build_id", buildID, "error", err)
	if ctx.Err() != nil {
		return
	}
	message := "the agent could not create the build: " + err.Error()
	truncated := truncate(message)
	report(&agentv1.BuildStatus{
		BuildId: buildID,
		Phase:   agentv1.BuildPhase_BUILD_PHASE_FAILED,
		Message: truncated,
	})
}
