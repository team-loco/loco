package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	regionMessageClusterSilent     = "the cluster running this region has stopped reporting"
	regionMessageClusterUnhealthy  = "the cluster running this region reports that it is unhealthy"
	regionMessageClusterNeverHeard = "the cluster running this region has never reported"
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
				EnvironmentName: row.EnvironmentName,
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
	if !row.Placed {
		return nil, fmt.Errorf("%w: deployment %s", errActiveDeploymentUnplaced, row.DeploymentID)
	}
	inFlight, err := deploymentInFlight(row.DeploymentStatus)
	if err != nil {
		return nil, err
	}
	region := &resourcev1.RegionStatus{
		Region:           row.Region,
		ReadyReplicas:    row.ReadyReplicas,
		DesiredReplicas:  row.DesiredReplicas,
		DeploymentId:     row.DeploymentID.String(),
		DeploymentStatus: deploymentStatusToProto(row.DeploymentStatus),
	}
	if row.StatusUpdatedAt != nil {
		region.ReportedAt = timestamppb.New(*row.StatusUpdatedAt)
	}

	if silence := clusterSilence(row, now, staleAfter); silence != "" {
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN
		region.Message = new(silence)
		return region, nil
	}

	switch {
	case row.ReadyReplicas >= row.DesiredReplicas:
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	case row.ReadyReplicas > 0:
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED
	case inFlight:
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING
	default:
		region.Status = resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE
	}
	region.Message = regionMessage(row, region.GetStatus())
	return region, nil
}

func deploymentInFlight(status genDb.DeploymentStatus) (bool, error) {
	switch status {
	case genDb.DeploymentStatusPending:
		return true, nil
	case genDb.DeploymentStatusDeploying:
		return true, nil
	case genDb.DeploymentStatusRunning:
		return false, nil
	case genDb.DeploymentStatusFailed:
		return false, nil
	case genDb.DeploymentStatusSucceeded:
		return false, fmt.Errorf("%w: %s", errFinishedDeploymentActive, status)
	case genDb.DeploymentStatusCanceled:
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

func regionMessage(row genDb.ListRegionStatusesForResourcesRow, status resourcev1.ResourceStatus) *string {
	if row.DeploymentStatus == genDb.DeploymentStatusFailed && row.DeploymentMessage != "" {
		return new(row.DeploymentMessage)
	}
	if status != resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY && row.StatusMessage != "" {
		return new(row.StatusMessage)
	}
	return nil
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
