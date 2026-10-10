package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

var (
	errUnknownOperation = errors.New("unknown plan operation")
	errWriteNotApplied  = errors.New("creates, imports and updates cannot be applied yet")
)

// applier performs the operations of one plan inside one transaction.
type applier struct {
	env             genDb.Environment
	partial         string
	live            liveEnvironment
	platformDomains []genDb.PlatformDomain
	defaults        servicedefaults.Defaults
	started         []*planv1.StartedDeployment
}

func (a *applier) apply(ctx context.Context, qtx *genDb.Queries, op configplan.Operation) error {
	switch op.Kind {
	case configplan.KindCreate:
		return connect.NewError(connect.CodeUnimplemented, errWriteNotApplied)
	case configplan.KindImport:
		return connect.NewError(connect.CodeUnimplemented, errWriteNotApplied)
	case configplan.KindUpdate:
		return connect.NewError(connect.CodeUnimplemented, errWriteNotApplied)
	case configplan.KindDelete:
		return a.delete(ctx, qtx, op)
	default:
		return fmt.Errorf("%w: %d", errUnknownOperation, op.Kind)
	}
}

func (a *applier) delete(ctx context.Context, qtx *genDb.Queries, op configplan.Operation) error {
	res := a.live.resources[op.Service]
	if a.live.elsewhere[res.ID] {
		return a.removeFromEnvironment(ctx, qtx, res)
	}
	if err := removeResourcePlacements(ctx, qtx, res.ID); err != nil {
		return err
	}
	if err := bumpResourceEnvironmentRevisions(ctx, qtx, res.ID); err != nil {
		return err
	}
	if err := qtx.DeleteResource(ctx, res.ID); err != nil {
		return fmt.Errorf("delete resource: %w", err)
	}
	return events.Record(ctx, qtx, events.Event{
		Type:        events.ResourceDeleted,
		WorkspaceID: new(res.WorkspaceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(res.ID),
		Data:        map[string]any{events.FieldName: res.Name, events.FieldPartial: a.partial},
	})
}

// removeFromEnvironment deletes what the environment holds of a service another environment
// still runs: its deployments and placements and its domains. The resource stays.
func (a *applier) removeFromEnvironment(ctx context.Context, qtx *genDb.Queries, res genDb.Resource) error {
	for _, deployment := range a.live.deployments[res.ID] {
		if err := removePlacement(ctx, qtx, res.ID, deployment.ClusterID); err != nil {
			return err
		}
		finalErr := qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
			ID:       deployment.ID,
			Status:   finalizedDeploymentStatus(deployment.Status),
			IsActive: false,
		})
		if finalErr != nil {
			return fmt.Errorf("finalize deployment %s: %w", deployment.ID, finalErr)
		}
		if err := events.Record(ctx, qtx, events.Event{
			Type:        events.DeploymentDeleted,
			WorkspaceID: new(res.WorkspaceID),
			SubjectType: events.SubjectDeployment,
			SubjectID:   new(deployment.ID),
			Data:        map[string]any{events.FieldResourceID: res.ID.String()},
		}); err != nil {
			return err
		}
	}
	for _, domain := range a.live.domains[res.ID] {
		if err := qtx.DeleteResourceDomain(ctx, domain.ID); err != nil {
			return fmt.Errorf("delete domain %s: %w", domain.Domain, err)
		}
		if err := events.Record(ctx, qtx, events.Event{
			Type:        events.DomainDeleted,
			WorkspaceID: new(res.WorkspaceID),
			SubjectType: events.SubjectDomain,
			SubjectID:   new(domain.ID),
			Data:        map[string]any{events.FieldDomain: domain.Domain},
		}); err != nil {
			return err
		}
	}
	return bumpEnvironmentRevision(ctx, qtx, a.env.ID)
}
