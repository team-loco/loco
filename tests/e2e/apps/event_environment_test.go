package apps

import (
	"testing"

	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
)

func TestAuditEnvironmentNames(t *testing.T) {
	t.Parallel()
	tenant := p.NewTenant(t)
	sibling := tenant.NewWorkspace(t, "sibling")
	for _, workspace := range []string{tenant.ID, sibling.ID} {
		_, err := p.User.Environment.CreateEnvironment(t.Context(), &environmentv1.CreateEnvironmentRequest{
			WorkspaceId: workspace, Name: "audit-stage", Type: environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	response, err := p.User.Event.ListEvents(
		t.Context(),
		&eventv1.ListEventsRequest{
			OrgId:           tenant.OrgID,
			EnvironmentName: "audit-stage",
			Query:           "@type:environment.created",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetEvents()) != 2 {
		t.Fatalf("environment events = %v", response.GetEvents())
	}
}
