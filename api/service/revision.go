package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
)

// Every transaction that changes a service's desired state or bumps an environment revision
// takes its row locks through the helpers in this file, coarse to fine, in one order:
//
//  1. the environments of the workspace, in ascending id (lockWorkspaceEnvironments or
//     lockResourceEnvironments)
//  2. the resources it writes, in ascending id (lockResources or lockResource)
//  3. the resource region, deployment, placement and domain rows of those resources
//
// A writer may skip a level it does not touch, never take one out of order, so two writers
// wait on each other in a line and never in a cycle.

// lockWorkspaceEnvironments locks every environment of the workspace in ascending id order.
func lockWorkspaceEnvironments(
	ctx context.Context,
	qtx *genDb.Queries,
	workspaceID uuid.UUID,
) error {
	if _, err := qtx.LockWorkspaceEnvironments(ctx, workspaceID); err != nil {
		return fmt.Errorf("lock environments: %w", err)
	}
	return nil
}

// lockResourceEnvironments is lockWorkspaceEnvironments for the workspace of a resource.
func lockResourceEnvironments(ctx context.Context, qtx *genDb.Queries, resourceID uuid.UUID) error {
	if _, err := qtx.LockResourceEnvironments(ctx, resourceID); err != nil {
		return fmt.Errorf("lock environments: %w", err)
	}
	return nil
}

// lockResources locks the resources in ascending id order.
func lockResources(ctx context.Context, qtx *genDb.Queries, ids []uuid.UUID) ([]uuid.UUID, error) {
	locked, err := qtx.LockResources(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("lock resources: %w", err)
	}
	return locked, nil
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
