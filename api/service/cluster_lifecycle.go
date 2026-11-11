package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/internal/rpcerr"
)

type clusterOperation string

const (
	clusterPause   clusterOperation = "pause"
	clusterResume  clusterOperation = "resume"
	clusterRemove  clusterOperation = "remove"
	clusterRestore clusterOperation = "restore"
)

var (
	ErrClusterRemoved      = errors.New("cluster is removed; restore it before resuming")
	ErrClusterNotRemoved   = errors.New("cluster is not removed")
	ErrClusterNotPaused    = errors.New("pause the cluster before removing it")
	ErrClusterHasWorkloads = errors.New(
		"cluster has workloads; move or stop them before removal, or use --force for a lost cluster",
	)
	ErrInvalidClusterOperation       = errors.New("cluster operation must be pause, resume, remove or restore")
	ErrClusterNotAssignable          = rpcerr.NewCoded(connect.CodeFailedPrecondition, "cluster is paused or removed")
	errClusterBuildsNotAllowed       = rpcerr.NewCoded(connect.CodeFailedPrecondition, "cluster does not accept builds")
	errClusterApplicationsNotAllowed = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"cluster does not accept applications",
	)
	ErrClusterNotEnrollable = errors.New(
		"cluster does not exist or is removed; restore it before issuing a join token",
	)
)

type clusterOperationResult struct {
	ClusterID uuid.UUID
	Operation clusterOperation
	Workloads []genDb.ListClusterWorkloadsRow
}

func manageCluster(
	ctx context.Context,
	pool *pgxpool.Pool,
	operation clusterOperation,
	id uuid.UUID,
	force bool,
) (clusterOperationResult, error) {
	result := clusterOperationResult{ClusterID: id, Operation: operation, Workloads: []genDb.ListClusterWorkloadsRow{}}
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
		case clusterPause:
			if cluster.RemovedAt != nil {
				return ErrClusterRemoved
			}
		case clusterResume:
			if cluster.RemovedAt != nil {
				return ErrClusterRemoved
			}
			params.Active = true
		case clusterRemove:
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
			if err := qtx.DeleteClusterRegistryCredentials(ctx, id); err != nil {
				return fmt.Errorf("delete registry credentials: %w", err)
			}
			params.RemoveCluster = true
		case clusterRestore:
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

func lockAssignableCluster(ctx context.Context, qtx *genDb.Queries, id uuid.UUID, build bool) error {
	cluster, err := qtx.LockAssignableCluster(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrClusterNotAssignable
	}
	if err != nil {
		return fmt.Errorf("lock assignable cluster: %w", err)
	}
	if build {
		if !cluster.BuildsAllowed || !cluster.BuildsEnabled {
			return errClusterBuildsNotAllowed
		}
	} else if !cluster.ApplicationsAllowed {
		return errClusterApplicationsNotAllowed
	}
	return nil
}
