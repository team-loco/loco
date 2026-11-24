package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
)

// Lock order: organization, workspace, environments by id, environment key, resources by id,
// clusters; their builds, deployments, placements last. A write locks the environments it changes,
// or every environment of the workspace when it changes a resource they share. Agent apply
// reports lock the resource only; a build report shares the resource's workspace, then locks the
// resource; a status report locks the resource, then shares its cluster's row to check the
// stream's sync generation. A transaction that deletes an environment
// holds every resource of its workspace first, or deletes only an environment without deployments.

// lockWorkspaceEnvironments locks every environment of the workspace in ascending id order.
func lockWorkspaceEnvironments(ctx context.Context, qtx *genDb.Queries, workspaceID uuid.UUID) error {
	if err := qtx.LockWorkspaceEnvironments(ctx, workspaceID); err != nil {
		return fmt.Errorf("lock environments: %w", err)
	}
	return nil
}

// lockResourceEnvironments is lockWorkspaceEnvironments for the workspace of a resource.
func lockResourceEnvironments(ctx context.Context, qtx *genDb.Queries, resourceID uuid.UUID) error {
	if err := qtx.LockResourceEnvironments(ctx, resourceID); err != nil {
		return fmt.Errorf("lock environments: %w", err)
	}
	return nil
}

// lockEnvironment locks one environment and returns its revision.
func lockEnvironment(ctx context.Context, qtx *genDb.Queries, environmentID uuid.UUID) (int64, error) {
	revision, err := qtx.LockEnvironment(ctx, environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrEnvironmentNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("lock environment: %w", err)
	}
	return revision, nil
}

// lockResources locks the resources in ascending id order.
func lockResources(ctx context.Context, qtx *genDb.Queries, ids []uuid.UUID) ([]uuid.UUID, error) {
	locked, err := qtx.LockResources(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("lock resources: %w", err)
	}
	return locked, nil
}

// lockOrgResources locks every resource of the organization in ascending id order.
func lockOrgResources(ctx context.Context, qtx *genDb.Queries, orgID uuid.UUID) error {
	if err := qtx.LockOrgResources(ctx, orgID); err != nil {
		return fmt.Errorf("lock resources: %w", err)
	}
	return nil
}

// lockResource locks one resource and returns ErrResourceNotFound when it does not exist.
func lockResource(ctx context.Context, qtx *genDb.Queries, id uuid.UUID) error {
	locked, err := lockResources(ctx, qtx, []uuid.UUID{id})
	if err != nil {
		return err
	}
	if len(locked) == 0 {
		return ErrResourceNotFound
	}
	return nil
}

func bumpEnvironmentRevision(ctx context.Context, qtx *genDb.Queries, environmentID uuid.UUID) error {
	if err := qtx.BumpEnvironmentRevision(ctx, environmentID); err != nil {
		return fmt.Errorf("bump environment revision: %w", err)
	}
	return nil
}

func bumpResourceEnvironmentRevisions(ctx context.Context, qtx *genDb.Queries, resourceID uuid.UUID) error {
	if err := qtx.BumpResourceEnvironmentRevisions(ctx, resourceID); err != nil {
		return fmt.Errorf("bump environment revisions: %w", err)
	}
	return nil
}

// txAttempts bounds how often withTx runs a transaction Postgres aborts to break a deadlock.
const txAttempts = 3

// withTx runs fn at read committed, so a count after a row lock sees rows committed while it
// waited, and runs it again when Postgres aborts it to break a deadlock.
func withTx(ctx context.Context, pool *pgxpool.Pool, fn func(qtx *genDb.Queries) error) error {
	return inTx(ctx, pool, func(tx pgx.Tx) error { return fn(genDb.New(tx)) })
}

// inTx is withTx for a function that needs the transaction itself.
func inTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	var err error
	for attempt := 1; attempt <= txAttempts; attempt++ {
		err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, fn)
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || pgErr.Code != pgDeadlockDetected {
			return err
		}
		slog.WarnContext(ctx, "transaction aborted to break a deadlock", "attempt", attempt, "sqlstate", pgErr.Code)
	}
	return err
}

func notifyCluster(ctx context.Context, qtx *genDb.Queries, clusterID uuid.UUID) error {
	clusterIDText := clusterID.String()
	if err := qtx.NotifyClusterPlacements(ctx, clusterIDText); err != nil {
		return fmt.Errorf("notify cluster %s: %w", clusterID, err)
	}
	return nil
}

// lockOrganization takes the organization FOR SHARE; SuspendOrg and ResumeOrg take it FOR UPDATE.
func lockOrganization(ctx context.Context, qtx *genDb.Queries, workspaceID uuid.UUID) error {
	if err := qtx.LockWorkspaceOrganization(ctx, workspaceID); err != nil {
		return fmt.Errorf("lock organization: %w", err)
	}
	return nil
}
