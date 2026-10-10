package service

import (
	"errors"
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestResourceStatusFromDeployments(t *testing.T) {
	cases := []struct {
		name   string
		active []genDb.DeploymentStatus
		want   resourcev1.ResourceStatus
	}{
		{name: "no deployment", want: resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED},
		{
			name:   "pending",
			active: []genDb.DeploymentStatus{genDb.DeploymentStatusPending},
			want:   resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
		},
		{
			name:   "one region rolling out",
			active: []genDb.DeploymentStatus{genDb.DeploymentStatusRunning, genDb.DeploymentStatusDeploying},
			want:   resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
		},
		{
			name:   "running everywhere",
			active: []genDb.DeploymentStatus{genDb.DeploymentStatusRunning, genDb.DeploymentStatusRunning},
			want:   resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		},
		{
			name:   "one region failed",
			active: []genDb.DeploymentStatus{genDb.DeploymentStatusRunning, genDb.DeploymentStatusFailed},
			want:   resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
		},
		{
			name:   "failed while another region deploys",
			active: []genDb.DeploymentStatus{genDb.DeploymentStatusDeploying, genDb.DeploymentStatusFailed},
			want:   resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
		},
		{
			name:   "failed everywhere",
			active: []genDb.DeploymentStatus{genDb.DeploymentStatusFailed, genDb.DeploymentStatusFailed},
			want:   resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resourceStatusFromDeployments(tc.active)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Fatalf("status = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestResourceStatusFromDeploymentsRejectsFinishedActiveDeployments(t *testing.T) {
	for _, status := range []genDb.DeploymentStatus{genDb.DeploymentStatusSucceeded, genDb.DeploymentStatusCanceled} {
		active := []genDb.DeploymentStatus{genDb.DeploymentStatusRunning, status}
		if _, err := resourceStatusFromDeployments(active); !errors.Is(err, errFinishedDeploymentActive) {
			t.Errorf("%s: err = %v, want %v", status, err, errFinishedDeploymentActive)
		}
	}
}
