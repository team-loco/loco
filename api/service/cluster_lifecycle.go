package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
)

const (
	ClusterPause   = "pause"
	ClusterResume  = "resume"
	ClusterRemove  = "remove"
	ClusterRestore = "restore"
)

var (
	ErrClusterRemoved      = errors.New("cluster is removed; restore it before resuming")
	ErrClusterNotRemoved   = errors.New("cluster is not removed")
	ErrClusterNotPaused    = errors.New("pause the cluster before removing it")
	ErrClusterHasWorkloads = errors.New(
		"cluster has workloads; move or stop them before removal, or use --force for a lost cluster",
	)
	ErrInvalidClusterOperation = errors.New("cluster operation must be pause, resume, remove or restore")
	ErrClusterNotAssignable    = errors.New("cluster is paused or removed")
)

type ClusterOperationResult struct {
	ClusterID uuid.UUID                       `json:"clusterId"`
	Operation string                          `json:"operation"`
	Workloads []genDb.ListClusterWorkloadsRow `json:"workloads"`
}

func ManageCluster(
	ctx context.Context,
	pool *pgxpool.Pool,
	operation string,
	id uuid.UUID,
	force bool,
) (ClusterOperationResult, error) {
	result := ClusterOperationResult{ClusterID: id, Operation: operation, Workloads: []genDb.ListClusterWorkloadsRow{}}
	err := withTx(ctx, pool, func(qtx *genDb.Queries) error {
		cluster, err := qtx.LockClusterLifecycle(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrClusterNotFound
		}
		if err != nil {
			return fmt.Errorf("lock cluster: %w", err)
		}
		params := genDb.UpdateClusterLifecycleParams{ID: id}
		switch operation {
		case ClusterPause:
			if cluster.RemovedAt != nil {
				return ErrClusterRemoved
			}
		case ClusterResume:
			if cluster.RemovedAt != nil {
				return ErrClusterRemoved
			}
			params.Active = true
		case ClusterRemove:
			if cluster.IsActive {
				return ErrClusterNotPaused
			}
			result.Workloads, err = qtx.ListClusterWorkloads(ctx, id)
			if err != nil {
				return fmt.Errorf("list workloads: %w", err)
			}
			if len(result.Workloads) > 0 && !force {
				return ErrClusterHasWorkloads
			}
			params.RemoveCluster = true
		case ClusterRestore:
			if cluster.RemovedAt == nil {
				return ErrClusterNotRemoved
			}
			params.RestoreCluster = true
		default:
			return ErrInvalidClusterOperation
		}
		if err := qtx.UpdateClusterLifecycle(ctx, params); err != nil {
			return fmt.Errorf("update cluster lifecycle: %w", err)
		}
		return notifyCluster(ctx, qtx, id)
	})
	return result, err
}

func lockAssignableCluster(ctx context.Context, qtx *genDb.Queries, id uuid.UUID) error {
	_, err := qtx.LockAssignableCluster(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrClusterNotAssignable
	}
	if err != nil {
		return fmt.Errorf("lock assignable cluster: %w", err)
	}
	return nil
}
