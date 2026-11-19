package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var errDeploymentFinished = rpcerr.NewCoded(
	connect.CodeFailedPrecondition,
	"the deployment finished or already runs in every region; roll back or redeploy instead",
)

// CancelDeployment stops a building or rolling-out deployment and records who canceled it.
func (s *DeploymentServer) CancelDeployment(
	ctx context.Context,
	req *deploymentv1.CancelDeploymentRequest,
) (*deploymentv1.CancelDeploymentResponse, error) {
	deploymentID := uuid.MustParse(req.GetDeploymentId())
	deployment, err := s.queries.GetDeploymentByID(ctx, deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeploymentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	required := actions.New(actions.CancelDeployment, deployment.ResourceID)
	if authErr := authorizeVisible(ctx, s.authz, required, ErrDeploymentNotFound); authErr != nil {
		return nil, authErr
	}
	principal, _ := reqctx.PrincipalFrom(ctx)
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		resource, getErr := qtx.GetResourceByID(ctx, deployment.ResourceID)
		if getErr != nil {
			return fmt.Errorf("get resource: %w", getErr)
		}
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, resource.WorkspaceID); lockErr != nil {
			return lockErr
		}
		if lockErr := lockResource(ctx, qtx, resource.ID); lockErr != nil {
			return lockErr
		}
		return s.cancelDeployment(ctx, qtx, resource, deploymentID, principal)
	})
	if err != nil {
		return nil, err
	}
	return &deploymentv1.CancelDeploymentResponse{}, nil
}

func (s *DeploymentServer) cancelDeployment(
	ctx context.Context,
	qtx *genDb.Queries,
	resource genDb.Resource,
	deploymentID uuid.UUID,
	principal reqctx.Principal,
) error {
	deployment, err := qtx.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("read deployment: %w", err)
	}
	if deployment.Status != genDb.DeploymentStatusBuilding && deployment.Status != genDb.DeploymentStatusDeploying {
		return errDeploymentFinished
	}
	placements, err := qtx.ListDeploymentPlacements(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("list deployment placements: %w", err)
	}
	render, err := newPlacementRenderer(ctx, qtx, resource, deployment.EnvironmentID, s.defaults)
	if err != nil {
		return err
	}
	reverted, kept := []string{}, []string{}
	for _, placement := range placements {
		if placement.StartedAt != nil {
			if slices.Contains(placementLiveStatuses, placement.Status) {
				kept = append(kept, placement.Region)
			}
			continue
		}
		if placement.Status != genDb.PlacementStatusPending && placement.Status != genDb.PlacementStatusDeploying {
			continue
		}
		if _, setErr := qtx.SetDeploymentPlacementStatus(ctx, genDb.SetDeploymentPlacementStatusParams{
			Status:       genDb.PlacementStatusCanceled,
			DeploymentID: deploymentID,
			ClusterID:    placement.ClusterID,
			FromStatuses: placementStatusNames(genDb.PlacementStatusPending, genDb.PlacementStatusDeploying),
		}); setErr != nil {
			return fmt.Errorf("cancel placement in %s: %w", placement.Region, setErr)
		}
		if placement.Status == genDb.PlacementStatusDeploying {
			if revertErr := revertPlacement(ctx, qtx, render, deployment, placement); revertErr != nil {
				return revertErr
			}
		}
		reverted = append(reverted, placement.Region)
	}
	if len(reverted) == 0 {
		return errDeploymentFinished
	}
	if setErr := qtx.SetDeploymentCanceledBy(ctx, genDb.SetDeploymentCanceledByParams{
		ID: deploymentID, ActorType: &principal.ActorType, ActorID: &principal.ActorID,
	}); setErr != nil {
		return fmt.Errorf("record who canceled deployment: %w", setErr)
	}
	if cancelErr := cancelUnusedBuild(ctx, qtx, deployment); cancelErr != nil {
		return cancelErr
	}
	if _, recomputeErr := recomputeDeploymentStatus(ctx, qtx, deploymentID); recomputeErr != nil {
		return fmt.Errorf("recompute canceled deployment: %w", recomputeErr)
	}
	if bumpErr := bumpEnvironmentRevision(ctx, qtx, deployment.EnvironmentID); bumpErr != nil {
		return bumpErr
	}
	return events.Record(ctx, qtx, events.Event{
		Type:          events.DeploymentCanceled,
		WorkspaceID:   new(resource.WorkspaceID),
		ResourceID:    new(resource.ID),
		EnvironmentID: new(deployment.EnvironmentID),
		SubjectType:   events.SubjectDeployment,
		SubjectID:     new(deploymentID),
		Data: map[string]any{
			events.FieldDeploymentID: deploymentID.String(),
			events.FieldReverted:     reverted,
			events.FieldKept:         kept,
		},
	})
}

// revertPlacement points the region back at the deployment that ran there before, or removes
// the Application when none did.
func revertPlacement(
	ctx context.Context,
	qtx *genDb.Queries,
	render placementRenderer,
	canceled genDb.Deployment,
	placement genDb.DeploymentPlacement,
) error {
	live, err := qtx.GetResourceEnvironmentPlacement(ctx, genDb.GetResourceEnvironmentPlacementParams{
		ResourceID: canceled.ResourceID, EnvironmentID: canceled.EnvironmentID, ClusterID: placement.ClusterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get placement in %s: %w", placement.Region, err)
	}
	if live.DeploymentID == nil || *live.DeploymentID != canceled.ID {
		return nil
	}
	previous, err := qtx.PreviousRunningDeploymentForCluster(ctx, genDb.PreviousRunningDeploymentForClusterParams{
		DeploymentID: canceled.ID, ClusterID: placement.ClusterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return removePlacement(ctx, qtx, canceled.ResourceID, canceled.EnvironmentID, placement.ClusterID)
	}
	if err != nil {
		return fmt.Errorf("find previous deployment in %s: %w", placement.Region, err)
	}
	spec, err := converter.DeserializeDeploymentSpec(previous.Spec, render.resource.Type)
	if err != nil {
		return fmt.Errorf("%w: decode deployment %s: %w", errDesiredSpec, previous.ID, err)
	}
	spec.GetService().Env, err = desiredEnv(ctx, qtx, canceled.ResourceID, canceled.EnvironmentID, placement.ClusterID)
	if err != nil {
		return err
	}
	target := placementTarget{region: placement.Region, clusterID: placement.ClusterID}
	if placeErr := placeDeployment(ctx, qtx, render, previous, target, spec); placeErr != nil {
		return fmt.Errorf("%w: %w", errDesiredSpec, placeErr)
	}
	return nil
}

// cancelUnusedBuild cancels the deployment's unfinished build unless another deployment in
// flight waits for or runs it.
func cancelUnusedBuild(ctx context.Context, qtx *genDb.Queries, deployment genDb.Deployment) error {
	spec, err := converter.DeserializeDeploymentSpec(deployment.Spec, genDb.ResourceTypeService)
	if err != nil {
		return fmt.Errorf("%w: decode deployment %s: %w", errDesiredSpec, deployment.ID, err)
	}
	rawBuildID := spec.GetService().GetBuild().GetBuildId()
	if rawBuildID == "" {
		return nil
	}
	used, err := qtx.BuildUsedByOtherDeployments(ctx, genDb.BuildUsedByOtherDeploymentsParams{
		BuildID: rawBuildID, DeploymentID: deployment.ID,
	})
	if err != nil {
		return fmt.Errorf("check build use: %w", err)
	}
	if used {
		return nil
	}
	_, err = cancelBuildTx(ctx, qtx, uuid.MustParse(rawBuildID), deployment.ResourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}
