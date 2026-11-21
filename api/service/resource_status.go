package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	clusterHealthHealthy   = "healthy"
	clusterHealthDegraded  = "degraded"
	clusterHealthUnhealthy = "unhealthy"
)

const (
	regionMessageClusterSilent     = "the cluster running this region has stopped reporting"
	regionMessageClusterUnhealthy  = "the cluster running this region reports that it is unhealthy"
	regionMessageClusterNeverHeard = "the cluster running this region has never reported"
	regionMessageDomainsUnserved   = "the cluster running this region does not serve "
)

var (
	errFinishedDeploymentActive = errors.New("finished deployment is still active")
	errUnknownDeploymentStatus  = errors.New("unknown deployment status")
	errNoDesiredReplicas        = errors.New("active deployment wants no replicas")
	errActiveDeploymentUnplaced = errors.New("active deployment has no placement")
	errNoRegions                = errors.New("environment status has no regions")
	errRegionStatusUnset        = errors.New("region status is unset")
)

func (s *ResourceServer) environmentStatuses(
	ctx context.Context,
	resourceIDs []uuid.UUID,
) (map[uuid.UUID][]*resourcev1.EnvironmentStatus, error) {
	rows, err := s.queries.ListRegionStatusesForResources(ctx, resourceIDs)
	if err != nil {
		return nil, fmt.Errorf("list region statuses: %w", err)
	}
	now := time.Now()
	statuses := make(map[uuid.UUID][]*resourcev1.EnvironmentStatus, len(resourceIDs))
	var current *resourcev1.EnvironmentStatus
	var currentResource uuid.UUID
	for _, row := range rows {
		region, regionErr := regionStatus(row, now, s.clusterSilentAfter)
		if regionErr != nil {
			return nil, fmt.Errorf("resource %s region %s: %w", row.ResourceID, row.Region, regionErr)
		}
		environmentID := row.EnvironmentID.String()
		if current == nil || currentResource != row.ResourceID || current.GetEnvironmentId() != environmentID {
			current = &resourcev1.EnvironmentStatus{
				EnvironmentId:   environmentID,
				EnvironmentSlug: row.EnvironmentSlug,
			}
			currentResource = row.ResourceID
			statuses[row.ResourceID] = append(statuses[row.ResourceID], current)
		}
		current.Regions = append(current.Regions, region)
	}
	for _, environments := range statuses {
		for _, environment := range environments {
			status, summaryErr := summarizeRegions(environment.GetRegions())
			if summaryErr != nil {
				return nil, summaryErr
			}
			environment.Status = status
			environment.StatusSince = environmentSince(environment.GetRegions(), status)
		}
	}
	return statuses, nil
}

func regionStatus(
	row genDb.ListRegionStatusesForResourcesRow,
	now time.Time,
	staleAfter time.Duration,
) (*resourcev1.RegionStatus, error) {
	if row.DesiredReplicas < 1 {
		return nil, fmt.Errorf("%w: deployment %s", errNoDesiredReplicas, row.DeploymentID)
	}
	if !row.Placed && row.PlacementStatus != genDb.PlacementStatusPending {
		return nil, fmt.Errorf("%w: deployment %s", errActiveDeploymentUnplaced, row.DeploymentID)
	}
	inFlight, err := placementInFlight(row.PlacementStatus)
	if err != nil {
		return nil, err
	}
	region := &resourcev1.RegionStatus{
		Region:          row.Region,
		DesiredReplicas: row.DesiredReplicas,
		DeploymentId:    row.DeploymentID.String(),
		PlacementStatus: placementStatuses[row.PlacementStatus],
	}
	if row.StatusUpdatedAt != nil {
		region.ReportedAt = timestamppb.New(*row.StatusUpdatedAt)
	}
	if decodeErr := json.Unmarshal(row.Replicas, &region.Replicas); decodeErr != nil {
		return nil, fmt.Errorf("decode observed replicas: %w", decodeErr)
	}
	region.ServingDeploymentId, region.ReadyReplicas = servingDeployment(region.GetReplicas(), region.GetDeploymentId())
	if region.GetServingDeploymentId() == "" && row.ReadyReplicas > 0 {
		// A ready count without the replicas behind it counts as the live deployment's.
		region.ServingDeploymentId, region.ReadyReplicas = region.GetDeploymentId(), row.ReadyReplicas
	}

	if silence := clusterSilence(row, now, staleAfter); silence != "" {
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN
		region.Message = new(silence)
		if row.LastHeartbeat != nil {
			region.StatusSince = timestamppb.New(*row.LastHeartbeat)
		}
		return region, nil
	}

	switch {
	case row.ReadyReplicas >= row.DesiredReplicas && row.PlacementStatus != genDb.PlacementStatusDegraded:
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	case row.ReadyReplicas > 0:
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED
	case inFlight:
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING
	default:
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE
	}
	region.Message, err = regionMessage(row, region.GetStatus())
	if err != nil {
		return nil, err
	}
	if region.GetStatus() == resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY && len(row.UnservedDomains) > 0 {
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED
		region.Message = new(regionMessageDomainsUnserved + strings.Join(row.UnservedDomains, ", "))
	}
	region.StatusSince = statusSince(row, region.GetStatus())
	return region, nil
}

// servingDeployment is the deployment whose revision has the most ready replicas, and that
// revision's ready count. A tie goes to the live deployment and then to the newer revision.
func servingDeployment(replicas []*deploymentv1.Replica, live string) (string, int32) {
	ready := make(map[servedRevision]int32)
	for _, replica := range replicas {
		if replica.GetReady() && replica.GetDeploymentId() != "" {
			ready[servedRevision{replica.GetDeploymentId(), replica.GetPlacementRevision()}]++
		}
	}
	var serving servedRevision
	for revision, count := range ready {
		if count > ready[serving] || count == ready[serving] && revision.newer(serving, live) {
			serving = revision
		}
	}
	return serving.deployment, ready[serving]
}

type servedRevision struct {
	deployment string
	revision   int64
}

// newer is whether r is newer than other, counting the live deployment newest. Deployment IDs are
// UUIDv7, so between two other deployments the greater ID is the newer.
func (r servedRevision) newer(other servedRevision, live string) bool {
	switch {
	case r.deployment == other.deployment:
		return r.revision > other.revision
	case other.deployment == live:
		return false
	case r.deployment == live:
		return true
	default:
		return r.deployment > other.deployment
	}
}

// statusSince is when the region entered status: the time recordRegionStatuses stamped, or the
// last report when the stamp belongs to another status.
func statusSince(row genDb.ListRegionStatusesForResourcesRow, status resourcev1.ResourceStatus) *timestamppb.Timestamp {
	since := row.StatusUpdatedAt
	if row.StoredStatus == status.String() && row.RegionStatusSince != nil {
		since = row.RegionStatusSince
	}
	if since == nil {
		return nil
	}
	return timestamppb.New(*since)
}

// recordRegionStatuses stamps the time each region of a resource entered its current status.
func recordRegionStatuses(
	ctx context.Context,
	q *genDb.Queries,
	resourceID uuid.UUID,
	staleAfter time.Duration,
) error {
	rows, err := q.ListRegionStatusesForResources(ctx, []uuid.UUID{resourceID})
	if err != nil {
		return fmt.Errorf("list region statuses: %w", err)
	}
	now := time.Now()
	for _, row := range rows {
		region, regionErr := regionStatus(row, now, staleAfter)
		if regionErr != nil {
			return fmt.Errorf("resource %s region %s: %w", row.ResourceID, row.Region, regionErr)
		}
		status := region.GetStatus().String()
		if status == row.StoredStatus {
			continue
		}
		if setErr := q.SetPlacementRegionStatus(ctx, genDb.SetPlacementRegionStatusParams{
			ResourceID:    row.ResourceID,
			EnvironmentID: row.EnvironmentID,
			ClusterID:     row.ClusterID,
			RegionStatus:  status,
		}); setErr != nil {
			return fmt.Errorf("set region status: %w", setErr)
		}
	}
	return nil
}

func placementInFlight(status genDb.PlacementStatus) (bool, error) {
	switch status {
	case genDb.PlacementStatusPending, genDb.PlacementStatusDeploying:
		return true, nil
	case genDb.PlacementStatusRunning, genDb.PlacementStatusDegraded, genDb.PlacementStatusCrashed,
		genDb.PlacementStatusFailed:
		return false, nil
	case genDb.PlacementStatusSuperseded, genDb.PlacementStatusCanceled:
		return false, fmt.Errorf("%w: %s", errFinishedDeploymentActive, status)
	default:
		return false, fmt.Errorf("%w: %s", errUnknownDeploymentStatus, status)
	}
}

func clusterSilence(row genDb.ListRegionStatusesForResourcesRow, now time.Time, staleAfter time.Duration) string {
	if row.LastHeartbeat == nil {
		return regionMessageClusterNeverHeard
	}
	if now.Sub(*row.LastHeartbeat) > staleAfter {
		return regionMessageClusterSilent
	}
	if row.HealthStatus == nil || *row.HealthStatus != clusterHealthHealthy {
		return regionMessageClusterUnhealthy
	}
	return ""
}

func regionMessage(row genDb.ListRegionStatusesForResourcesRow, status resourcev1.ResourceStatus) (*string, error) {
	var problem *agentv1.ProblemReport
	if err := json.Unmarshal(row.Problem, &problem); err != nil {
		return nil, fmt.Errorf("decode stored rollout: %w", err)
	}
	if problem != nil && (status != resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY ||
		row.PlacementStatus == genDb.PlacementStatusFailed) {
		return new(problemReportMessage(problem)), nil
	}
	return nil, nil
}

func summarizeRegions(regions []*resourcev1.RegionStatus) (resourcev1.ResourceStatus, error) {
	if len(regions) == 0 {
		return resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED, errNoRegions
	}
	first := regions[0].GetStatus()
	uniform := true
	failing := false
	unknown := false
	for _, region := range regions {
		status := region.GetStatus()
		if status != first {
			uniform = false
		}
		switch status {
		case resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE:
			failing = true
		case resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED:
			failing = true
		case resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN:
			unknown = true
		case resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY:
		case resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING:
		case resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED:
			return resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED,
				fmt.Errorf("%w: region %s", errRegionStatusUnset, region.GetRegion())
		}
	}
	switch {
	case uniform:
		return first, nil
	case failing:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED, nil
	case unknown:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN, nil
	default:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING, nil
	}
}

// environmentSince is when the environment entered status: the earliest time a region entered a
// failing status, or the latest time a region entered a healthy or deploying one.
func environmentSince(regions []*resourcev1.RegionStatus, status resourcev1.ResourceStatus) *timestamppb.Timestamp {
	failing := status == resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED ||
		status == resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE ||
		status == resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN
	var since *timestamppb.Timestamp
	for _, region := range regions {
		contributes := region.GetStatus() == status ||
			status == resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED &&
				region.GetStatus() == resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE
		at := region.GetStatusSince()
		if !contributes || at == nil {
			continue
		}
		if since == nil || failing && at.AsTime().Before(since.AsTime()) ||
			!failing && at.AsTime().After(since.AsTime()) {
			since = at
		}
	}
	return since
}
