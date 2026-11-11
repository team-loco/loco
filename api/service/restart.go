package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func (s *ResourceServer) RestartResource(
	ctx context.Context,
	req *resourcev1.RestartResourceRequest,
) (*resourcev1.RestartResourceResponse, error) {
	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.RestartResource, resourceID); err != nil {
		return nil, err
	}
	environmentID, err := parseID("environment_id", req.GetEnvironmentId())
	if err != nil {
		return nil, err
	}
	res, err := s.queries.GetResourceByID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get resource: %w", err)
	}
	if res.Type != genDb.ResourceTypeService {
		return nil, errOnlyServiceResources
	}
	env, err := s.queries.GetEnvironmentByID(ctx, environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEnvironmentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	if env.WorkspaceID != res.WorkspaceID {
		return nil, ErrEnvironmentNotFound
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := ensureOrganizationActive(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, res.WorkspaceID); lockErr != nil {
			return lockErr
		}
		if lockErr := lockResource(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		if rolloutErr := ensureNoRollout(ctx, qtx, resourceID, environmentID, req.GetRegion()); rolloutErr != nil {
			return rolloutErr
		}
		// The controller copies the placement revision into the pod template, so a new
		// revision rolls replicas without changing the desired spec or deployment ID.
		clusters, restartErr := qtx.RestartResourcePlacements(ctx, genDb.RestartResourcePlacementsParams{
			ResourceID: resourceID, EnvironmentID: environmentID, Region: req.GetRegion(),
		})
		if restartErr != nil {
			return fmt.Errorf("restart placements: %w", restartErr)
		}
		if len(clusters) == 0 {
			if req.GetRegion() != "" {
				return errNoDeploymentInRegion.Detail("%s", req.GetRegion())
			}
			return errNoActiveDeployment
		}
		for _, clusterID := range clusters {
			if notifyErr := notifyCluster(ctx, qtx, clusterID); notifyErr != nil {
				return notifyErr
			}
		}
		data := map[string]any{events.FieldEnvironmentID: environmentID.String()}
		if req.Region != nil {
			data[events.FieldRegion] = req.GetRegion()
		}
		return events.Record(ctx, qtx, events.Event{
			Type: events.ResourceRestarted, WorkspaceID: &res.WorkspaceID, EnvironmentID: &environmentID,
			SubjectType: events.SubjectResource, SubjectID: &resourceID, Data: data,
		})
	})
	if err != nil {
		return nil, err
	}
	return &resourcev1.RestartResourceResponse{}, nil
}
