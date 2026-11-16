package service

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
)

func TestAuditEnvironmentNamesAcrossWorkspaces(t *testing.T) {
	t.Parallel()
	f := newScopedAuditFixture(t)
	orgID := uuid.MustParse(f.orgID)
	org, err := f.queries.GetOrgByID(t.Context(), orgID)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := f.queries.CreateWorkspace(
		t.Context(),
		genDb.CreateWorkspaceParams{OrgID: f.otherOrgID, Name: "foreign", CreatedBy: org.CreatedBy},
	)
	if err != nil {
		t.Fatal(err)
	}
	var want []int64
	var ids []uuid.UUID
	const name = "stage'_%"
	for _, workspace := range []uuid.UUID{f.workspaceID, f.otherWorkspaceID, foreign} {
		ownerOrg := orgID
		if workspace == foreign {
			ownerOrg = f.otherOrgID
		}
		env, createErr := f.queries.CreateEnvironment(t.Context(), genDb.CreateEnvironmentParams{
			WorkspaceID: workspace, Name: name, EnvironmentType: "staging", CreatedBy: org.CreatedBy,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		seq, insertErr := f.queries.InsertEvent(t.Context(), genDb.InsertEventParams{
			Type:          events.ResourceDegraded,
			OrgID:         &ownerOrg,
			WorkspaceID:   &workspace,
			EnvironmentID: &env.ID,
			ActorType:     events.ActorSystem,
			Data:          []byte(`{}`),
		})
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		if workspace != foreign {
			want = append(want, seq)
			ids = append(ids, env.ID)
		}
	}
	slices.Reverse(want)
	req := &eventv1.ListEventsRequest{OrgId: f.orgID, EnvironmentName: name, PageSize: 1}
	var got []int64
	for {
		res, listErr := f.events.ListEvents(f.owner, req)
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, event := range res.GetEvents() {
			got = append(got, event.GetSeq())
		}
		req.BeforeSeq = res.GetNextBeforeSeq()
		if req.GetBeforeSeq() == 0 {
			break
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for _, tc := range []struct {
		name, workspace, environment string
		count                        int
	}{
		{name, f.workspaceID.String(), "", 1},
		{name, "", ids[0].String(), 1},
		{"different-environment", "", ids[0].String(), 0},
		{"stage", "", "", 0},
	} {
		res, listErr := f.events.ListEvents(
			f.owner,
			&eventv1.ListEventsRequest{
				OrgId:           f.orgID,
				EnvironmentName: tc.name,
				WorkspaceId:     tc.workspace,
				EnvironmentId:   tc.environment,
			},
		)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(res.GetEvents()) != tc.count {
			t.Fatalf("filter %+v: events = %v", tc, res.GetEvents())
		}
	}
}
