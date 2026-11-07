package appwatch

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/agent/pkg/kube"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

type Watcher struct {
	reader    client.Reader
	secrets   client.Reader
	namespace string
	feed      *kube.Feed[*agentv1.PlacementStatus]
	cache     *kube.Cache
}

func Start(ctx context.Context, cfg *rest.Config, namespace string) (*Watcher, error) {
	scheme, err := kube.NewScheme()
	if err != nil {
		return nil, err
	}
	secrets, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create Secret client: %w", err)
	}
	w := newWatcher(nil, secrets, namespace)
	c, err := kube.StartCache(ctx, cfg, scheme, namespace, &locoControllerV1.Application{}, w.observe, w.forget)
	if err != nil {
		return nil, err
	}
	// observe and forget never read reader, so it can be set after the cache starts.
	w.reader, w.cache = c, c
	return w, nil
}

// Wait blocks until ctx ends or the Application watch stops for good, and returns why.
func (w *Watcher) Wait(ctx context.Context) error {
	return w.cache.Wait(ctx)
}

func newWatcher(reader, secrets client.Reader, namespace string) *Watcher {
	return &Watcher{
		reader:    reader,
		secrets:   secrets,
		namespace: namespace,
		feed:      kube.NewFeed[*agentv1.PlacementStatus](),
	}
}

// WithInventory calls fn with the inventory while no status is forwarded, so the API never
// receives an inventory older than a status it already has.
func (w *Watcher) WithInventory(ctx context.Context, fn func(*agentv1.Inventory) error) error {
	return w.feed.Freeze(func() error {
		inventory, err := w.inventory(ctx)
		if err != nil {
			return err
		}
		return fn(inventory)
	})
}

// Attach replays every known placement status to sink and forwards later changes to it.
func (w *Watcher) Attach(sink func(*agentv1.PlacementStatus)) func() {
	return w.feed.Attach(sink)
}

// inventory lists the placement revision of every Application. An Application that references a
// staging Secret not yet labeled with the referenced revision is reported with EnvSecretPending,
// so the API resends an Apply that stopped between its two writes instead of recording it as
// applied, and can still advance past a revision the cluster holds ahead of the database.
func (w *Watcher) inventory(ctx context.Context) (*agentv1.Inventory, error) {
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
		placementID, revision, ok := app.Placement()
		if !ok {
			continue
		}
		entries = append(entries, &agentv1.InventoryEntry{
			PlacementId:      placementID,
			Revision:         revision,
			EnvSecretPending: !envSecretStaged(app, staged),
		})
	}
	return &agentv1.Inventory{Entries: entries}, nil
}

func (w *Watcher) stagedRevisions(ctx context.Context) (map[string]int64, error) {
	var secrets corev1.SecretList
	hasPlacement := client.HasLabels{locoControllerV1.KeyPlacementID}
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

func (w *Watcher) observe(app *locoControllerV1.Application) {
	placementID, _, ok := app.Placement()
	if !ok {
		return
	}
	w.feed.Publish(placementID, &agentv1.PlacementStatus{
		PlacementId:      placementID,
		ObservedRevision: app.Status.ObservedPlacementRevision,
		Ready:            app.Status.Phase == locoControllerV1.ApplicationPhaseReady,
		ReadyReplicas:    app.Status.ReadyReplicas,
		Phase:            app.Status.Phase,
		Message:          app.Status.Message,
	})
}

func (w *Watcher) forget(app *locoControllerV1.Application) {
	placementID, _, ok := app.Placement()
	if !ok {
		return
	}
	w.feed.Forget(placementID)
}
