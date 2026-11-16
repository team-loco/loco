package apps

import (
	"testing"

	webhookv1 "github.com/team-loco/loco/gen/go/loco/webhook/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestWebhookDeliveryCounts(t *testing.T) {
	t.Parallel()
	tenant := p.NewTenant(t)
	created, err := p.User.Webhook.CreateWebhook(t.Context(), &webhookv1.CreateWebhookRequest{
		WorkspaceId: tenant.ID, Url: "https://hooks.invalid/loco-e2e", EventTypes: []string{"workspace.updated"},
	})
	if err != nil {
		t.Fatal(err)
	}
	hook := created.GetWebhook().GetId()
	_, err = p.User.Workspace.UpdateWorkspace(
		t.Context(),
		&workspacev1.UpdateWorkspaceRequest{WorkspaceId: tenant.ID, Description: new("delivery counts")},
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := p.User.Webhook.ListWebhookDeliveries(
		t.Context(),
		&webhookv1.ListWebhookDeliveriesRequest{WorkspaceId: tenant.ID, WebhookId: hook, PageSize: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetTotalCount() != 1 || response.GetMatchingCount() != 1 || len(response.GetDeliveries()) != 1 {
		t.Fatalf("delivery counts = %v", response)
	}
	failed, err := p.User.Webhook.ListWebhookDeliveries(
		t.Context(),
		&webhookv1.ListWebhookDeliveriesRequest{WorkspaceId: tenant.ID, WebhookId: hook, Failing: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if failed.GetTotalCount() != 1 || failed.GetMatchingCount() != failed.GetFailingCount() ||
		int64(len(failed.GetDeliveries())) != failed.GetMatchingCount() {
		t.Fatalf("failing counts = %v", failed)
	}
	_, err = p.User.Webhook.DeleteWebhook(
		t.Context(),
		&webhookv1.DeleteWebhookRequest{WorkspaceId: tenant.ID, WebhookId: hook},
	)
	if err != nil {
		t.Fatal(err)
	}
}
