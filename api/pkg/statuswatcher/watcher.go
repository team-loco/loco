package statuswatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/allegro/bigcache/v3"
	"github.com/jackc/pgx/v5/pgtype"
	genDb "github.com/loco-team/loco/api/gen/db"
	"github.com/loco-team/loco/api/pkg/kube"
	locoControllerV1 "github.com/team-loco/loco/controller/api/v1alpha1"
	"k8s.io/client-go/tools/cache"
	crClient "sigs.k8s.io/controller-runtime/pkg/client"
)

type StatusWatcher struct {
	kubeClient              *kube.Client
	queries                 genDb.Querier
	lastKnownStatus         *bigcache.BigCache
	lastKnownResourceStatus *bigcache.BigCache
	locoNamespace           string
}

func NewStatusWatcher(kubeClient *kube.Client, queries genDb.Querier) *StatusWatcher {
	statusCache, _ := bigcache.New(context.Background(), bigcache.DefaultConfig(24*time.Hour))
	resourceStatusCache, _ := bigcache.New(context.Background(), bigcache.DefaultConfig(24*time.Hour))

	return &StatusWatcher{
		kubeClient:              kubeClient,
		queries:                 queries,
		lastKnownStatus:         statusCache,
		lastKnownResourceStatus: resourceStatusCache,
		locoNamespace:           os.Getenv("LOCO_NAMESPACE"),
	}
}

func (w *StatusWatcher) Start(ctx context.Context) error {
	slog.InfoContext(ctx, "starting status watcher")

	go func() {
		if err := w.kubeClient.Manager.Start(ctx); err != nil {
			slog.ErrorContext(ctx, "manager start error", "error", err)
			panic(err)
		}
	}()

	locoInformer, err := w.kubeClient.Cache.GetInformer(ctx, &locoControllerV1.Application{})
	if err != nil {
		return err
	}

	if !cache.WaitForCacheSync(ctx.Done(), locoInformer.HasSynced) {
		slog.ErrorContext(ctx, "failed to wait for cache sync")
		return ctx.Err()
	}

	if err := w.backfill(ctx); err != nil {
		slog.ErrorContext(ctx, "backfill failed", "error", err)
		return err
	}

	locoInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj any) {
			oldLR := oldObj.(*locoControllerV1.Application)
			newLR := newObj.(*locoControllerV1.Application)
			if !reflect.DeepEqual(oldLR.Status, newLR.Status) {
				w.syncToDB(ctx, newLR)
			}
		},
	})

	<-ctx.Done()
	return ctx.Err()
}

func (w *StatusWatcher) backfill(ctx context.Context) error {
	slog.InfoContext(ctx, "backfilling deployment statuses")

	resourceIDs, err := w.queries.ListActiveDeployments(ctx)
	if err != nil {
		return fmt.Errorf("failed to list active deployments: %w", err)
	}

	slog.InfoContext(ctx, "found active deployments", "count", len(resourceIDs))

	for _, resourceID := range resourceIDs {
		locoRes := &locoControllerV1.Application{}
		key := crClient.ObjectKey{
			Name:      fmt.Sprintf("resource-%d", resourceID),
			Namespace: w.locoNamespace,
		}
		if err := w.kubeClient.ControllerClient.Get(ctx, key, locoRes); err != nil {
			slog.WarnContext(ctx, "failed to get Application", "resourceId", resourceID, "error", err)
			continue
		}
		slog.DebugContext(ctx, "syncing resource", "resourceId", resourceID, "phase", locoRes.Status.Phase)
		w.syncToDB(ctx, locoRes)
	}

	slog.InfoContext(ctx, "backfill completed", "count", len(resourceIDs))
	return nil
}

func (w *StatusWatcher) syncToDB(ctx context.Context, locoRes *locoControllerV1.Application) {
	if locoRes.Spec.ResourceId == 0 {
		slog.WarnContext(ctx, "skipping sync: Application has no resourceId", "name", locoRes.Name)
		return
	}

	status := convertPhase(locoRes.Status.Phase)
	message := locoRes.Status.Message

	key := strconv.FormatInt(locoRes.Spec.ResourceId, 10)
	cached, err := w.lastKnownStatus.Get(key)
	if err == nil {
		var last struct{ phase, message string }
		if json.Unmarshal(cached, &last) == nil {
			if last.phase == locoRes.Status.Phase && last.message == message {
				return
			}
		}
	}

	err = w.queries.UpdateActiveDeploymentStatus(ctx, genDb.UpdateActiveDeploymentStatusParams{
		ResourceID: locoRes.Spec.ResourceId,
		Status:     status,
		Message:    pgtype.Text{String: message, Valid: message != ""},
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update deployment status",
			"error", err,
			"resourceId", locoRes.Spec.ResourceId,
			"phase", locoRes.Status.Phase,
		)
		return
	}

	slog.InfoContext(ctx, "updated deployment status",
		"resourceId", locoRes.Spec.ResourceId,
		"phase", locoRes.Status.Phase,
	)

	data, _ := json.Marshal(struct{ phase, message string }{
		phase:   locoRes.Status.Phase,
		message: message,
	})
	w.lastKnownStatus.Set(key, data)

	w.syncResourceStatus(ctx, locoRes.Spec.ResourceId)
}

func (w *StatusWatcher) syncResourceStatus(ctx context.Context, resourceID int64) {
	deploymentStatuses, err := w.queries.ListActiveDeploymentsByResourceID(ctx, resourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list deployments for resource",
			"error", err,
			"resourceId", resourceID,
		)
		return
	}

	computedStatus := computeResourceStatus(deploymentStatuses)

	key := strconv.FormatInt(resourceID, 10)
	cached, err := w.lastKnownResourceStatus.Get(key)
	if err == nil {
		if string(cached) == string(computedStatus) {
			return
		}
	}

	err = w.queries.UpdateResourceStatus(ctx, genDb.UpdateResourceStatusParams{
		ID:     resourceID,
		Status: computedStatus,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update resource status",
			"error", err,
			"resourceId", resourceID,
			"status", computedStatus,
		)
		return
	}

	slog.InfoContext(ctx, "updated resource status",
		"resourceId", resourceID,
		"status", computedStatus,
	)

	w.lastKnownResourceStatus.Set(key, []byte(computedStatus))
}

func computeResourceStatus(deploymentStatuses []genDb.DeploymentStatus) genDb.ResourceStatus {
	if len(deploymentStatuses) == 0 {
		return genDb.ResourceStatusHealthy
	}

	hasRunning := false
	hasFailed := false
	hasDeploying := false

	for _, status := range deploymentStatuses {
		switch status {
		case genDb.DeploymentStatusFailed:
			hasFailed = true
		case genDb.DeploymentStatusDeploying:
			hasDeploying = true
		case genDb.DeploymentStatusRunning:
			hasRunning = true
		}
	}

	if hasFailed && !hasRunning {
		return genDb.ResourceStatusUnavailable
	}
	if hasDeploying {
		return genDb.ResourceStatusDeploying
	}
	if hasFailed && hasRunning {
		return genDb.ResourceStatusDegraded
	}
	if hasRunning {
		return genDb.ResourceStatusHealthy
	}

	return genDb.ResourceStatusHealthy
}

func convertPhase(phase string) genDb.DeploymentStatus {
	switch phase {
	case "Idle":
		return genDb.DeploymentStatusPending
	case "Deploying":
		return genDb.DeploymentStatusDeploying
	case "Ready":
		return genDb.DeploymentStatusRunning
	case "Failed":
		return genDb.DeploymentStatusFailed
	default:
		return genDb.DeploymentStatusPending
	}
}
