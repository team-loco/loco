package resource

import (
	"bytes"
	"strings"
	"testing"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const statusTestRegion = "us-ca-1"

func TestStatusViewShowsEachEnvironmentAndRegion(t *testing.T) {
	crash := "A replica keeps restarting. Check its logs."
	pull := `Back-off pulling image "registry.test/web:v2": ImagePullBackOff`
	resource := &resourcev1.Resource{
		Slug: "web",
		EnvironmentStatuses: []*resourcev1.EnvironmentStatus{
			{
				EnvironmentSlug: "production",
				Status:          resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
				Regions: []*resourcev1.RegionStatus{
					{
						Region:          "us-va-1",
						Status:          resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
						ReadyReplicas:   0,
						DesiredReplicas: 2,
						PlacementStatus: deploymentv1.PlacementStatus_PLACEMENT_STATUS_FAILED,
						Message:         &crash,
					},
					{
						Region:          statusTestRegion,
						Status:          resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
						ReadyReplicas:   2,
						DesiredReplicas: 2,
						PlacementStatus: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
					},
				},
			},
			{
				EnvironmentSlug: "staging",
				Status:          resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
				Regions: []*resourcev1.RegionStatus{
					{
						Region:          statusTestRegion,
						Status:          resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
						ReadyReplicas:   1,
						DesiredReplicas: 1,
						PlacementStatus: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
						Message:         &pull,
					},
				},
			},
		},
	}

	var out bytes.Buffer
	if err := renderStatusView(&out, "web", resource); err != nil {
		t.Fatalf("renderStatusView: %v", err)
	}
	view := out.String()
	for _, want := range []string{
		"production", "Degraded", "us-va-1", "Unavailable", "0/2", "failed", crash,
		statusTestRegion, "Healthy", "2/2", "running",
		"staging", "Unknown", "1/1", pull,
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
	resource := &resourcev1.Resource{Slug: "web"}
	var out bytes.Buffer
	if err := renderStatusView(&out, "web", resource); err != nil {
		t.Fatalf("renderStatusView: %v", err)
	}
	if !strings.Contains(out.String(), notDeployedMessage) {
		t.Errorf("view does not say the service is not deployed:\n%s", out.String())
	}
}

func TestReplicaStateLabelCountsRestartsAgainstTheOnFailureBudget(t *testing.T) {
	restarting := &deploymentv1.Replica{State: "Failing", RestartsAt: []*timestamppb.Timestamp{timestamppb.Now()}}
	for _, tt := range []struct {
		policy *deploymentv1.FailurePolicy
		want   string
	}{
		{&deploymentv1.FailurePolicy{
			RestartPolicy: deploymentv1.RestartPolicy_RESTART_POLICY_ON_FAILURE, MaxRestarts: 3,
		}, "Restarting (1/3)"},
		{&deploymentv1.FailurePolicy{RestartPolicy: deploymentv1.RestartPolicy_RESTART_POLICY_ALWAYS}, "Restarting (1)"},
	} {
		if got := replicaStateLabel(restarting, tt.policy); got != tt.want {
			t.Errorf("%s label = %q, want %q", tt.policy.GetRestartPolicy(), got, tt.want)
		}
	}
}

func TestStatusViewSeparatesPublicationFromWorkloadHealth(t *testing.T) {
	for _, tc := range []struct {
		state domainv1.PublicationState
		want  string
	}{
		{domainv1.PublicationState_PUBLICATION_STATE_OPERATOR_MANAGED, "Operator managed"},
		{domainv1.PublicationState_PUBLICATION_STATE_PENDING, "Pending"},
		{domainv1.PublicationState_PUBLICATION_STATE_PUBLISHED, "Published"},
		{domainv1.PublicationState_PUBLICATION_STATE_ERROR, "Error"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			res := &resourcev1.Resource{
				Domains: []*domainv1.ResourceDomain{
					{Domain: "web.example.com", Publication: &domainv1.DomainPublication{State: tc.state}},
				},
			}
			var out bytes.Buffer
			if err := renderStatusView(&out, "web", res); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"web.example.com", "Public routing:", tc.want, notDeployedMessage} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("missing %q: %s", want, out.String())
				}
			}
		})
	}
}
