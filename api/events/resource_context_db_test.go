package events_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func TestRecordPersistsResourceIdentityWithoutCurrentChildren(t *testing.T) {
	pool := authtest.NewPool(t)
	q := genDb.New(pool)
	var resourceID, workspaceID, orgID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
 WITH u AS (INSERT INTO users(email) VALUES ('audit@example.test') RETURNING id),
 o AS (INSERT INTO organizations(name,created_by) SELECT 'audit',id FROM u RETURNING id),
 w AS (INSERT INTO workspaces(org_id,name,created_by) SELECT o.id,'audit',u.id FROM o,u RETURNING id,org_id),
 r AS (INSERT INTO resources(workspace_id,name,type,description,spec,spec_version)
 SELECT id,'audit','service','','{}',1 FROM w RETURNING id,workspace_id)
 SELECT r.id,w.id,w.org_id FROM r,w`).Scan(&resourceID, &workspaceID, &orgID); err != nil {
		t.Fatal(err)
	}
	cases := []events.Event{
		{Type: events.ResourceDeleted, SubjectType: events.SubjectResource, SubjectID: &resourceID},
		{
			Type:        events.DeploymentDeleted,
			SubjectType: events.SubjectDeployment,
			SubjectID:   new(uuid.New()),
			ResourceID:  &resourceID,
		},
		{
			Type:        events.DomainDeleted,
			SubjectType: events.SubjectDomain,
			SubjectID:   new(uuid.New()),
			ResourceID:  &resourceID,
		},
		{
			Type:        events.BuildCanceled,
			SubjectType: events.SubjectBuild,
			SubjectID:   new(uuid.New()),
			ResourceID:  &resourceID,
		},
		{Type: events.SecretDeleted, SubjectType: events.SubjectResource, SubjectID: &resourceID},
	}
	for _, event := range cases {
		t.Run(event.Type, func(t *testing.T) {
			event.WorkspaceID = &workspaceID
			event.OrgID = &orgID
			if err := events.Record(t.Context(), q, event); err != nil {
				t.Fatal(err)
			}
			var actual uuid.UUID
			if err := pool.QueryRow(t.Context(), "SELECT resource_id FROM events WHERE type=$1", event.Type).
				Scan(&actual); err != nil {
				t.Fatal(err)
			}
			if actual != resourceID {
				t.Fatalf("resource=%s, want %s", actual, resourceID)
			}
		})
	}
	deleted, err := pool.Exec(t.Context(), "DELETE FROM resources WHERE id=$1", resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.RowsAffected() != 1 {
		t.Fatalf("deleted %d resources, want one", deleted.RowsAffected())
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM events WHERE resource_id=$1", resourceID).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(cases) {
		t.Fatalf("retained %d events, want %d", count, len(cases))
	}
}
