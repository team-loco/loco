package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

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

// CancelDeployment stops a building or rolling-out deployment and records who canceled it. With
// rollback_finished_regions it also deploys, to each region the deployment already runs, the
// version that region ran before.
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
	var rollbacks []uuid.UUID
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		resource, getErr := qtx.GetResourceByID(ctx, deployment.ResourceID)
		if getErr != nil {
			return fmt.Errorf("get resource: %w", getErr)
		}
		if req.GetRollbackFinishedRegions() {
			if lockErr := lockOrganization(ctx, qtx, resource.WorkspaceID); lockErr != nil {
				return lockErr
			}
		}
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, resource.WorkspaceID); lockErr != nil {
			return lockErr
		}
		if lockErr := lockResource(ctx, qtx, resource.ID); lockErr != nil {
			return lockErr
		}
		var cancelErr error
		rollbacks, cancelErr = s.cancelDeployment(
			ctx, qtx, resource, deploymentID, principal, req.GetRollbackFinishedRegions(),
		)
		return cancelErr
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rollbacks))
	for _, id := range rollbacks {
		ids = append(ids, id.String())
	}
	return &deploymentv1.CancelDeploymentResponse{RollbackDeploymentIds: ids}, nil
}

// GetDeploymentCancelImpact lists the regions a building or rolling-out deployment already runs,
// with the deployment each ran before.
func (s *DeploymentServer) GetDeploymentCancelImpact(
	ctx context.Context,
	req *deploymentv1.GetDeploymentCancelImpactRequest,
) (*deploymentv1.GetDeploymentCancelImpactResponse, error) {
	deploymentID := uuid.MustParse(req.GetDeploymentId())
	deployment, err := s.queries.GetDeploymentByID(ctx, deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeploymentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	required := actions.New(actions.GetDeployment, deployment.ResourceID)
	if authErr := authorizeVisible(ctx, s.authz, required, ErrDeploymentNotFound); authErr != nil {
		return nil, authErr
	}
	if deployment.Status != genDb.DeploymentStatusBuilding && deployment.Status != genDb.DeploymentStatusDeploying {
		return nil, errDeploymentFinished
	}
	placements, err := s.queries.ListDeploymentPlacements(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("list deployment placements: %w", err)
	}
	finished := []*deploymentv1.FinishedRegion{}
	for _, placement := range finishedPlacements(placements) {
		region := &deploymentv1.FinishedRegion{Region: placement.Region}
		previous, prevErr := s.queries.PreviousLiveDeploymentForCluster(
			ctx,
			genDb.PreviousLiveDeploymentForClusterParams{
				DeploymentID: deploymentID, ClusterID: placement.ClusterID,
			},
		)
		if prevErr != nil && !errors.Is(prevErr, pgx.ErrNoRows) {
			return nil, fmt.Errorf("find previous deployment in %s: %w", placement.Region, prevErr)
		}
		if prevErr == nil {
			view, viewErr := s.queries.GetDeploymentView(ctx, previous.ID)
			if viewErr != nil {
				return nil, fmt.Errorf("get previous deployment %s: %w", previous.ID, viewErr)
			}
			if region.Previous, viewErr = deploymentToProto(view); viewErr != nil {
				return nil, viewErr
			}
		}
		finished = append(finished, region)
	}
	return &deploymentv1.GetDeploymentCancelImpactResponse{FinishedRegions: finished}, nil
}

// finishedPlacements are the regions that already run the deployment and keep it on a cancel.
func finishedPlacements(placements []genDb.DeploymentPlacement) []genDb.DeploymentPlacement {
	var finished []genDb.DeploymentPlacement
	for _, placement := range placements {
		if placement.StartedAt != nil && slices.Contains(placementLiveStatuses, placement.Status) {
			finished = append(finished, placement)
		}
	}
	return finished
}

func (s *DeploymentServer) cancelDeployment(
	ctx context.Context,
	qtx *genDb.Queries,
	resource genDb.Resource,
	deploymentID uuid.UUID,
	principal reqctx.Principal,
	rollback bool,
) ([]uuid.UUID, error) {
	deployment, err := qtx.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("read deployment: %w", err)
	}
	if deployment.Status != genDb.DeploymentStatusBuilding && deployment.Status != genDb.DeploymentStatusDeploying {
		return nil, errDeploymentFinished
	}
	placements, err := qtx.ListDeploymentPlacements(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("list deployment placements: %w", err)
	}
	render, err := newPlacementRenderer(ctx, qtx, resource, deployment.EnvironmentID, s.defaults)
	if err != nil {
		return nil, err
	}
	finished := finishedPlacements(placements)
	reverted, kept := []string{}, []string{}
	for _, placement := range finished {
		kept = append(kept, placement.Region)
	}
	for _, placement := range placements {
		if placement.StartedAt != nil {
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
			return nil, fmt.Errorf("cancel placement in %s: %w", placement.Region, setErr)
		}
		if placement.Status == genDb.PlacementStatusDeploying {
			if revertErr := revertPlacement(ctx, qtx, render, deployment, placement); revertErr != nil {
				return nil, revertErr
			}
		}
		reverted = append(reverted, placement.Region)
	}
	if len(reverted) == 0 {
		return nil, errDeploymentFinished
	}
	if setErr := qtx.SetDeploymentCanceledBy(ctx, genDb.SetDeploymentCanceledByParams{
		ID: deploymentID, ActorType: &principal.ActorType, ActorID: &principal.ActorID,
	}); setErr != nil {
		return nil, fmt.Errorf("record who canceled deployment: %w", setErr)
	}
	if cancelErr := cancelUnusedBuild(ctx, qtx, deployment); cancelErr != nil {
		return nil, cancelErr
	}
	if _, recomputeErr := recomputeDeploymentStatus(ctx, qtx, deploymentID); recomputeErr != nil {
		return nil, fmt.Errorf("recompute canceled deployment: %w", recomputeErr)
	}
	rollbacks := []uuid.UUID{}
	if rollback {
		if rollbacks, err = rollbackFinishedRegions(ctx, qtx, render, deployment, finished, principal); err != nil {
			return nil, err
		}
	}
	if bumpErr := bumpEnvironmentRevision(ctx, qtx, deployment.EnvironmentID); bumpErr != nil {
		return nil, bumpErr
	}
	if err = events.Record(ctx, qtx, events.Event{
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
			events.FieldRollbacks:    rollbacks,
		},
	}); err != nil {
		return nil, err
	}
	return rollbacks, nil
}

// rollbackFinishedRegions deploys, to each finished region with an earlier deployment that ran
// there, that deployment's spec: one rollback deployment per earlier deployment.
func rollbackFinishedRegions(
	ctx context.Context,
	qtx *genDb.Queries,
	render placementRenderer,
	canceled genDb.Deployment,
	finished []genDb.DeploymentPlacement,
	principal reqctx.Principal,
) ([]uuid.UUID, error) {
	var previousIDs []uuid.UUID
	previous := map[uuid.UUID]genDb.Deployment{}
	targets := map[uuid.UUID][]placementTarget{}
	for _, placement := range finished {
		prev, err := qtx.PreviousLiveDeploymentForCluster(ctx, genDb.PreviousLiveDeploymentForClusterParams{
			DeploymentID: canceled.ID, ClusterID: placement.ClusterID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("find previous deployment in %s: %w", placement.Region, err)
		}
		if _, seen := previous[prev.ID]; !seen {
			previousIDs = append(previousIDs, prev.ID)
			previous[prev.ID] = prev
		}
		targets[prev.ID] = append(
			targets[prev.ID],
			placementTarget{region: placement.Region, clusterID: placement.ClusterID},
		)
	}
	rollbacks := []uuid.UUID{}
	for _, id := range previousIDs {
		restored, err := rollbackRegions(ctx, qtx, render, canceled, previous[id], targets[id], principal)
		if err != nil {
			return nil, err
		}
		rollbacks = append(rollbacks, restored)
	}
	return rollbacks, nil
}

// rollbackRegions creates a rollback deployment of previous to the regions the canceled
// deployment already runs.
func rollbackRegions(
	ctx context.Context,
	qtx *genDb.Queries,
	render placementRenderer,
	canceled, previous genDb.Deployment,
	targets []placementTarget,
	principal reqctx.Principal,
) (uuid.UUID, error) {
	spec, err := converter.DeserializeDeploymentSpec(previous.Spec, render.resource.Type)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: decode deployment %s: %w", errDesiredSpec, previous.ID, err)
	}
	service := spec.GetService()
	if lockErr := lockPinnedBuildImage(ctx, qtx, service.GetBuild()); lockErr != nil {
		return uuid.Nil, lockErr
	}
	regions := make(map[string]*deploymentv1.RegionDeploymentTarget, len(targets))
	for _, target := range targets {
		regions[target.region] = service.GetRegions()[target.region]
	}
	service.Regions = regions
	restored, err := createDeployment(ctx, qtx, nil, newDeployment{
		params: genDb.CreateDeploymentParams{
			ResourceID:             canceled.ResourceID,
			EnvironmentID:          canceled.EnvironmentID,
			SecretNames:            previous.SecretNames,
			ActorType:              principal.ActorType,
			ActorID:                principal.ActorID,
			Method:                 principal.Method,
			CommitSha:              previous.CommitSha,
			CommitDirty:            previous.CommitDirty,
			Trigger:                genDb.DeploymentTriggerRollback,
			SourceDeploymentID:     &previous.ID,
			RollbackOfDeploymentID: &canceled.ID,
		},
		service: service,
		targets: targets,
		render:  render.payload,
	})
	if err != nil {
		return uuid.Nil, err
	}
	regionNames := targetRegions(targets)
	if err = recordDeploymentCreated(
		ctx,
		qtx,
		render.resource,
		canceled.EnvironmentID,
		restored,
		regionNames,
	); err != nil {
		return uuid.Nil, err
	}
	if err = writeDeploymentActivity(ctx, qtx, canceled.ID, activityScope{}, []activityEntry{{
		Message:  "Rolled back " + strings.Join(regionNames, ", ") + " to " + shortDeploymentID(previous.ID),
		Severity: activityWarn,
	}}); err != nil {
		return uuid.Nil, err
	}
	if err = writeDeploymentActivity(ctx, qtx, restored, activityScope{}, []activityEntry{{
		Message:  "Rollback of " + shortDeploymentID(canceled.ID) + " after it was canceled",
		Severity: activityInfo,
	}}); err != nil {
		return uuid.Nil, err
	}
	return restored, nil
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
