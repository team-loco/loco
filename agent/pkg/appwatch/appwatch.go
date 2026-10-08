package appwatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"google.golang.org/protobuf/proto"
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
	c, err := cache.New(cfg, cache.Options{
		Scheme:            scheme,
		DefaultNamespaces: map[string]cache.Config{namespace: {}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Application cache: %w", err)
	}

	w := New(c, namespace)
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

func New(reader client.Reader, namespace string) *Watcher {
	return &Watcher{
		reader:    reader,
		namespace: namespace,
		statuses:  make(map[string]*agentv1.PlacementStatus),
	}
}

func (w *Watcher) Inventory(ctx context.Context) (*agentv1.Inventory, error) {
	var apps locoControllerV1.ApplicationList
	if err := w.reader.List(ctx, &apps, client.InNamespace(w.namespace)); err != nil {
		return nil, fmt.Errorf("failed to list Applications: %w", err)
	}
	entries := make([]*agentv1.InventoryEntry, 0, len(apps.Items))
	for i := range apps.Items {
		placement, ok := applier.PlacementOf(&apps.Items[i])
		if !ok {
			continue
		}
		entries = append(entries, &agentv1.InventoryEntry{
			PlacementId: placement.ID,
			Revision:    placement.Revision,
		})
	}
	return &agentv1.Inventory{Entries: entries}, nil
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
