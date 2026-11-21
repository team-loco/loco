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
	regionMessageClusterSilent     = "the cluster running this region has stopped reporting"
	regionMessageClusterUnhealthy  = "the cluster running this region reports that it is unhealthy"
	regionMessageClusterNeverHeard = "the cluster running this region has never reported"
	regionMessageDomainsUnserved   = "the cluster running this region does not serve "
)

// The causes region_status_cause in the migrations returns.
const (
	statusCauseClusterNeverReported = "cluster_never_reported"
	statusCauseClusterSilent        = "cluster_silent"
	statusCauseClusterUnhealthy     = "cluster_unhealthy"
	statusCauseDomainsUnserved      = "domains_unserved"
)

var clusterCauseMessages = map[string]string{
	statusCauseClusterNeverReported: regionMessageClusterNeverHeard,
	statusCauseClusterSilent:        regionMessageClusterSilent,
	statusCauseClusterUnhealthy:     regionMessageClusterUnhealthy,
}

var (
	errFinishedDeploymentActive = errors.New("finished deployment is still active")
	errUnknownDeploymentStatus  = errors.New("unknown deployment status")
	errNoDesiredReplicas        = errors.New("active deployment wants no replicas")
	errActiveDeploymentUnplaced = errors.New("active deployment has no placement")
	errUnknownResourceStatus    = errors.New("unknown resource status")
)

func (s *ResourceServer) environmentStatuses(
	ctx context.Context,
	resourceIDs []uuid.UUID,
) (map[uuid.UUID][]*resourcev1.EnvironmentStatus, error) {
	rows, err := s.queries.ListRegionStatusesForResources(ctx, genDb.ListRegionStatusesForResourcesParams{
		ResourceIds:        resourceIDs,
		SilentAfterSeconds: s.clusterSilentAfter.Seconds(),
	})
	if err != nil {
		return nil, fmt.Errorf("list region statuses: %w", err)
	}
	statuses := make(map[uuid.UUID][]*resourcev1.EnvironmentStatus, len(resourceIDs))
	var current *resourcev1.EnvironmentStatus
	var currentResource uuid.UUID
	for _, row := range rows {
		region, regionErr := regionStatus(row)
		if regionErr != nil {
			return nil, fmt.Errorf("resource %s region %s: %w", row.ResourceID, row.Region, regionErr)
		}
		environmentID := row.EnvironmentID.String()
		if current == nil || currentResource != row.ResourceID || current.GetEnvironmentId() != environmentID {
			environment, statusErr := parseResourceStatus(row.EnvironmentStatus)
			if statusErr != nil {
				return nil, fmt.Errorf("resource %s environment %s: %w", row.ResourceID, row.EnvironmentSlug, statusErr)
			}
			current = &resourcev1.EnvironmentStatus{
				EnvironmentId:   environmentID,
				EnvironmentSlug: row.EnvironmentSlug,
				Status:          environment,
			}
			currentResource = row.ResourceID
			statuses[row.ResourceID] = append(statuses[row.ResourceID], current)
		}
		current.Regions = append(current.Regions, region)
	}
	for _, environments := range statuses {
		for _, environment := range environments {
			environment.StatusSince = environmentSince(environment.GetRegions(), environment.GetStatus())
		}
	}
	return statuses, nil
}

func regionStatus(row genDb.ListRegionStatusesForResourcesRow) (*resourcev1.RegionStatus, error) {
	if row.DesiredReplicas < 1 {
		return nil, fmt.Errorf("%w: deployment %s", errNoDesiredReplicas, row.DeploymentID)
	}
	if !row.Placed && row.PlacementStatus != genDb.PlacementStatusPending {
		return nil, fmt.Errorf("%w: deployment %s", errActiveDeploymentUnplaced, row.DeploymentID)
	}
	if err := checkPlacementActive(row.PlacementStatus); err != nil {
		return nil, err
	}
	status, err := parseResourceStatus(row.Status)
	if err != nil {
		return nil, err
	}
	region := &resourcev1.RegionStatus{
		Region:               row.Region,
		DesiredReplicas:      row.DesiredReplicas,
		DeploymentId:         row.DeploymentID.String(),
		LastGoodDeploymentId: row.LastGoodDeploymentID,
		PlacementStatus:      placementStatuses[row.PlacementStatus],
		Status:               status,
	}
	if row.StatusUpdatedAt != nil {
		region.ReportedAt = timestamppb.New(*row.StatusUpdatedAt)
	}
	if decodeErr := json.Unmarshal(row.Replicas, &region.Replicas); decodeErr != nil {
		return nil, fmt.Errorf("decode observed replicas: %w", decodeErr)
	}
	region.ReadyReplicas = min(row.ReadyReplicas, row.DesiredReplicas)
	region.ServingDeploymentId = servingDeployment(region.GetReplicas(), region.GetDeploymentId())
	if region.GetServingDeploymentId() == "" && row.ReadyReplicas > 0 && row.PlacedDeploymentID != nil {
		// A ready count without the replicas behind it counts as the placed deployment's.
		region.ServingDeploymentId = row.PlacedDeploymentID.String()
	}

	if message, ok := clusterCauseMessages[row.StatusCause]; ok {
		region.Message = new(message)
		if row.LastHeartbeat != nil {
			region.StatusSince = timestamppb.New(*row.LastHeartbeat)
		}
		return region, nil
	}
	if row.StatusCause == statusCauseDomainsUnserved {
		region.Message = new(regionMessageDomainsUnserved + strings.Join(row.UnservedDomains, ", "))
	} else if region.Message, err = regionMessage(row, status); err != nil {
		return nil, err
	}
	region.StatusSince = statusSince(row, status)
	return region, nil
}

// parseResourceStatus reads a status name the status SQL functions return.
func parseResourceStatus(name string) (resourcev1.ResourceStatus, error) {
	value, ok := resourcev1.ResourceStatus_value[name]
	if !ok || value == int32(resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED) {
		return 0, fmt.Errorf("%w: %q", errUnknownResourceStatus, name)
	}
	return resourcev1.ResourceStatus(value), nil
}

// servingDeployment is the deployment with the most ready replicas. A tie goes to the live
// deployment, then to the greater UUIDv7, the newer.
func servingDeployment(replicas []*deploymentv1.Replica, live string) string {
	ready := make(map[string]int)
	for _, replica := range replicas {
		if replica.GetReady() && replica.GetDeploymentId() != "" {
			ready[replica.GetDeploymentId()]++
		}
	}
	serving := ""
	for id, count := range ready {
		best := ready[serving]
		if count > best || count == best && serving != live && (id == live || id > serving) {
			serving = id
		}
	}
	return serving
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
	silentAfter time.Duration,
) error {
	rows, err := q.ListRegionStatusesForResources(ctx, genDb.ListRegionStatusesForResourcesParams{
		ResourceIds:        []uuid.UUID{resourceID},
		SilentAfterSeconds: silentAfter.Seconds(),
	})
	if err != nil {
		return fmt.Errorf("list region statuses: %w", err)
	}
	for _, row := range rows {
		region, regionErr := regionStatus(row)
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

func checkPlacementActive(status genDb.PlacementStatus) error {
	switch status {
	case genDb.PlacementStatusPending, genDb.PlacementStatusDeploying, genDb.PlacementStatusRunning,
		genDb.PlacementStatusDegraded, genDb.PlacementStatusCrashed, genDb.PlacementStatusFailed:
		return nil
	case genDb.PlacementStatusSuperseded, genDb.PlacementStatusCanceled:
		return fmt.Errorf("%w: %s", errFinishedDeploymentActive, status)
	default:
		return fmt.Errorf("%w: %s", errUnknownDeploymentStatus, status)
	}
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
