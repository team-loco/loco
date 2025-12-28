package statuswatcher

import (
	"context"
	"log/slog"
	"reflect"

	"github.com/jackc/pgx/v5/pgtype"
	genDb "github.com/loco-team/loco/api/gen/db"
	"github.com/loco-team/loco/api/pkg/kube"
	locoControllerV1 "github.com/team-loco/loco/controller/api/v1alpha1"
	"k8s.io/client-go/tools/cache"
)

type StatusWatcher struct {
	kubeClient      *kube.Client
	queries         genDb.Querier
	lastKnownStatus map[int64]struct{ phase, message string }
}

func NewStatusWatcher(kubeClient *kube.Client, queries genDb.Querier) *StatusWatcher {
	return &StatusWatcher{
		kubeClient:      kubeClient,
		queries:         queries,
		lastKnownStatus: make(map[int64]struct{ phase, message string }),
	}
}

func (w *StatusWatcher) Start(ctx context.Context) error {
	slog.InfoContext(ctx, "starting status watcher")

	go func() {
		if err := w.kubeClient.Manager.Start(ctx); err != nil {
			slog.ErrorContext(ctx, "manager start error", "error", err)
		}
	}()

	locoInformer, err := w.kubeClient.Cache.GetInformer(ctx, &locoControllerV1.LocoResource{})
	if err != nil {
		return err
	}

	if !cache.WaitForCacheSync(ctx.Done(), locoInformer.HasSynced) {
		return ctx.Err()
	}

	locoInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj any) {
			oldLR := oldObj.(*locoControllerV1.LocoResource)
			newLR := newObj.(*locoControllerV1.LocoResource)
			if !reflect.DeepEqual(oldLR.Status, newLR.Status) {
				w.syncToDB(ctx, newLR)
			}
		},
	})

	<-ctx.Done()
	return ctx.Err()
}

func (w *StatusWatcher) syncToDB(ctx context.Context, locoRes *locoControllerV1.LocoResource) {
	if locoRes.Spec.ResourceId == 0 {
		slog.WarnContext(ctx, "skipping sync: LocoResource has no resourceId", "name", locoRes.Name)
		return
	}

	status := convertPhase(locoRes.Status.Phase)

	message := locoRes.Status.Message
	if locoRes.Status.ErrorMessage != "" {
		message = locoRes.Status.ErrorMessage
	}

	last, exists := w.lastKnownStatus[locoRes.Spec.ResourceId]
	if exists && last.phase == locoRes.Status.Phase && last.message == message {
		return
	}

	err := w.queries.UpdateActiveDeploymentStatus(ctx, genDb.UpdateActiveDeploymentStatusParams{
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

	w.lastKnownStatus[locoRes.Spec.ResourceId] = struct{ phase, message string }{
		phase:   locoRes.Status.Phase,
		message: message,
	}
}

func convertPhase(phase string) genDb.DeploymentStatus {
	switch phase {
	case "Idle":
		return genDb.DeploymentStatusPending
	case "Deploying":
		return genDb.DeploymentStatusPending
	case "Ready":
		return genDb.DeploymentStatusRunning
	case "Failed":
		return genDb.DeploymentStatusFailed
	default:
		return genDb.DeploymentStatusPending
	}
}
