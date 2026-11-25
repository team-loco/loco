package webhooks

import (
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func TestNotificationFor(t *testing.T) {
	t.Parallel()
	org, workspace, resource := uuid.New(), uuid.New(), uuid.New()
	names := genDb.GetEventNamesRow{OrgName: "acme", WorkspaceName: "storefront", ResourceName: "web"}
	resourceURL := fmt.Sprintf("https://app.loco.test/org/%s/wks/%s/resource/%s", org, workspace, resource)
	tests := []struct {
		name       string
		row        genDb.GetWebhookDeliveryPayloadRow
		names      genDb.GetEventNamesRow
		title      string
		message    string
		priority   int
		click      string
		wantTagged string
	}{
		{
			name: "build failure",
			row: genDb.GetWebhookDeliveryPayloadRow{
				Type: events.BuildFailed, OrgID: &org, WorkspaceID: &workspace, ResourceID: &resource,
				Data: []byte(`{"message":"exit code 1"}`),
			},
			names:      names,
			title:      "Build failed",
			message:    "Build of acme / storefront / web failed: exit code 1",
			priority:   4,
			click:      resourceURL,
			wantTagged: "x",
		},
		{
			name: "deployment failure",
			row: genDb.GetWebhookDeliveryPayloadRow{
				Type: events.DeploymentFailed, OrgID: &org, WorkspaceID: &workspace, ResourceID: &resource,
				Data: []byte(`{"message":"deploy timed out"}`),
			},
			names:      names,
			title:      "Deployment failed",
			message:    "Deployment of acme / storefront / web failed: deploy timed out",
			priority:   4,
			click:      resourceURL,
			wantTagged: "x",
		},
		{
			name: "cluster silent",
			row: genDb.GetWebhookDeliveryPayloadRow{
				Type: events.ClusterSilent, Data: []byte(`{"name":"edge-1","region":"us-ca-1"}`),
			},
			title:      "Cluster silent",
			message:    "Cluster edge-1 (us-ca-1) stopped reporting.",
			priority:   5,
			wantTagged: "rotating_light",
		},
		{
			name: "cluster recovered",
			row: genDb.GetWebhookDeliveryPayloadRow{
				Type: events.ClusterRecovered, Data: []byte(`{"name":"edge-1","region":"us-ca-1","silentSeconds":600}`),
			},
			title:      "Cluster recovered",
			message:    "Cluster edge-1 (us-ca-1) reports again after 10m0s.",
			priority:   3,
			wantTagged: "white_check_mark",
		},
		{
			name: "cluster health changed",
			row: genDb.GetWebhookDeliveryPayloadRow{
				Type: events.ClusterHealthChanged,
				Data: []byte(`{"name":"edge-1","region":"us-ca-1","old":"healthy","new":"degraded"}`),
			},
			title:      "Cluster health changed",
			message:    "Cluster edge-1 (us-ca-1) went from healthy to degraded.",
			priority:   3,
			wantTagged: "warning",
		},
		{
			name: "organization suspended",
			row: genDb.GetWebhookDeliveryPayloadRow{
				Type: events.OrgSuspended, OrgID: &org, Data: []byte(`{"reason":"unpaid invoice"}`),
			},
			names:      genDb.GetEventNamesRow{OrgName: "acme"},
			title:      "Organization suspended",
			message:    "acme was suspended: unpaid invoice",
			priority:   3,
			click:      "https://app.loco.test/org/" + org.String() + "/audit",
			wantTagged: "no_entry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := notificationFor(tt.row, tt.names, "https://app.loco.test/")
			if err != nil {
				t.Fatal(err)
			}
			if got.Title != tt.title || got.Message != tt.message || got.Priority != tt.priority ||
				got.Click != tt.click || !slices.Contains(got.Tags, tt.wantTagged) {
				t.Errorf("notification = %+v, want title %q, message %q, priority %d, click %q, tag %q",
					got, tt.title, tt.message, tt.priority, tt.click, tt.wantTagged)
			}
		})
	}
}

func TestNotificationHasNoLinkWithoutDashboardURL(t *testing.T) {
	t.Parallel()
	org, workspace, resource := uuid.New(), uuid.New(), uuid.New()
	row := genDb.GetWebhookDeliveryPayloadRow{
		Type: events.BuildFailed, OrgID: &org, WorkspaceID: &workspace, ResourceID: &resource, Data: []byte(`{}`),
	}
	got, err := notificationFor(row, genDb.GetEventNamesRow{}, "")
	if err != nil || got.Click != "" {
		t.Fatalf("notification = %+v, err %v, want no click link", got, err)
	}
}
