package resource

import (
	"bytes"
	"strings"
	"testing"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const statusTestRegion = "us-east-1"

func TestStatusViewShowsEachEnvironmentAndRegion(t *testing.T) {
	crash := "container app is crash looping"
	resp := &resourcev1.GetResourceStatusResponse{
		Resource: &resourcev1.Resource{
			Name: "web",
			EnvironmentStatuses: []*resourcev1.EnvironmentStatus{
				{
					EnvironmentName: "production",
					Status:          resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
					Regions: []*resourcev1.RegionStatus{
						{
							Region:           "eu-west-1",
							Status:           resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
							ReadyReplicas:    0,
							DesiredReplicas:  2,
							DeploymentStatus: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED,
							Message:          &crash,
						},
						{
							Region:           statusTestRegion,
							Status:           resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
							ReadyReplicas:    2,
							DesiredReplicas:  2,
							DeploymentStatus: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
						},
					},
				},
				{
					EnvironmentName: "staging",
					Status:          resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
					Regions: []*resourcev1.RegionStatus{
						{
							Region:           statusTestRegion,
							Status:           resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
							ReadyReplicas:    1,
							DesiredReplicas:  1,
							DeploymentStatus: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
						},
					},
				},
			},
		},
	}

	var out bytes.Buffer
	if err := renderStatusView(&out, "web", resp); err != nil {
		t.Fatalf("renderStatusView: %v", err)
	}
	view := out.String()
	for _, want := range []string{
		"production", "Degraded", "eu-west-1", "Unavailable", "0/2", "failed", crash,
		statusTestRegion, "Healthy", "2/2", "running",
		"staging", "Unknown", "1/1",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
	if strings.Index(view, "production") > strings.Index(view, "staging") {
		t.Errorf("environments are out of order:\n%s", view)
	}
}

func TestStatusViewSaysWhenNothingIsDeployed(t *testing.T) {
	resp := &resourcev1.GetResourceStatusResponse{Resource: &resourcev1.Resource{Name: "web"}}
	var out bytes.Buffer
	if err := renderStatusView(&out, "web", resp); err != nil {
		t.Fatalf("renderStatusView: %v", err)
	}
	if !strings.Contains(out.String(), notDeployedMessage) {
		t.Errorf("view does not say the service is not deployed:\n%s", out.String())
	}
}
