package appwatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/agent/pkg/applier"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const phaseReady = "Ready"

var errCacheNotSynced = errors.New("application cache did not sync")

type Sink func(*agentv1.PlacementStatus)

type Watcher struct {
	reader    client.Reader
	secrets   client.Reader
	namespace string

	mu       sync.Mutex
	statuses map[string]*agentv1.PlacementStatus
	sink     Sink
	sinkID   uint64
}

func Start(ctx context.Context, cfg *rest.Config, namespace string) (*Watcher, error) {
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add loco types to scheme: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add core types to scheme: %w", err)
	}
	secrets, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create Secret client: %w", err)
	}
	c, err := cache.New(cfg, cache.Options{
		Scheme:            scheme,
		DefaultNamespaces: map[string]cache.Config{namespace: {}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Application cache: %w", err)
	}

	w := New(c, secrets, namespace)
	informer, err := c.GetInformer(ctx, &locoControllerV1.Application{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Application informer: %w", err)
	}
	_, err = informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    w.Observe,
		UpdateFunc: w.observeUpdate,
		DeleteFunc: w.Forget,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to watch Applications: %w", err)
	}

	go func() {
		if startErr := c.Start(ctx); startErr != nil {
			slog.ErrorContext(ctx, "Application cache stopped", "error", startErr)
		}
	}()
	if !c.WaitForCacheSync(ctx) {
		return nil, errCacheNotSynced
	}
	return w, nil
}

// New creates a Watcher that lists Applications from reader and staging Secrets from secrets.
func New(reader, secrets client.Reader, namespace string) *Watcher {
	return &Watcher{
		reader:    reader,
		secrets:   secrets,
		namespace: namespace,
		statuses:  make(map[string]*agentv1.PlacementStatus),
	}
}

// Inventory lists the placement revision of every Application. An Application that references a
// staging Secret not yet labeled with the referenced revision is reported with EnvSecretPending,
// so the API resends an Apply that stopped between its two writes instead of recording it as
// applied, and can still advance past a revision the cluster holds ahead of the database.
func (w *Watcher) Inventory(ctx context.Context) (*agentv1.Inventory, error) {
	var apps locoControllerV1.ApplicationList
	if err := w.reader.List(ctx, &apps, client.InNamespace(w.namespace)); err != nil {
		return nil, fmt.Errorf("failed to list Applications: %w", err)
	}
	staged, err := w.stagedRevisions(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]*agentv1.InventoryEntry, 0, len(apps.Items))
	for i := range apps.Items {
		app := &apps.Items[i]
		placement, ok := applier.PlacementOf(app)
		if !ok {
			continue
		}
		entries = append(entries, &agentv1.InventoryEntry{
			PlacementId:      placement.ID,
			Revision:         placement.Revision,
			EnvSecretPending: !envSecretStaged(app, staged),
		})
	}
	return &agentv1.Inventory{Entries: entries}, nil
}

func (w *Watcher) stagedRevisions(ctx context.Context) (map[string]int64, error) {
	var secrets corev1.SecretList
	hasPlacement := client.HasLabels{locoControllerV1.LabelPlacementID}
	if err := w.secrets.List(ctx, &secrets, client.InNamespace(w.namespace), hasPlacement); err != nil {
		return nil, fmt.Errorf("failed to list env Secrets: %w", err)
	}
	revisions := make(map[string]int64, len(secrets.Items))
	for i := range secrets.Items {
		revision, ok := locoControllerV1.EnvSecretRevision(secrets.Items[i].Labels)
		if ok {
			revisions[secrets.Items[i].Name] = revision
		}
	}
	return revisions, nil
}

func envSecretStaged(app *locoControllerV1.Application, staged map[string]int64) bool {
	spec := app.Spec.ServiceSpec
	if spec == nil || spec.Deployment == nil || spec.Deployment.EnvSecretRef == nil {
		return true
	}
	ref := spec.Deployment.EnvSecretRef
	revision, ok := staged[ref.Name]
	return ok && revision == ref.Revision
}

func (w *Watcher) Attach(sink Sink) func() {
	w.mu.Lock()
	w.sinkID++
	id := w.sinkID
	w.sink = sink
	snapshot := make([]*agentv1.PlacementStatus, 0, len(w.statuses))
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

func (w *Watcher) observeUpdate(_, obj any) {
	w.Observe(obj)
}

func (w *Watcher) Observe(obj any) {
	app, ok := obj.(*locoControllerV1.Application)
	if !ok {
		return
	}
	placement, ok := applier.PlacementOf(app)
	if !ok {
		return
	}
	status := &agentv1.PlacementStatus{
		PlacementId:      placement.ID,
		ObservedRevision: app.Status.ObservedPlacementRevision,
		Ready:            app.Status.Phase == phaseReady,
		Phase:            app.Status.Phase,
		Message:          app.Status.Message,
	}

	w.mu.Lock()
	previous := w.statuses[placement.ID]
	if proto.Equal(previous, status) {
		w.mu.Unlock()
		return
	}
	w.statuses[placement.ID] = status
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
	app, ok := obj.(*locoControllerV1.Application)
	if !ok {
		return
	}
	placement, ok := applier.PlacementOf(app)
	if !ok {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.statuses, placement.ID)
}
