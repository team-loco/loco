package service

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
)

type scopedAuditFixture struct {
	*eventFixture
	workspaceID      uuid.UUID
	resourceID       uuid.UUID
	otherResourceID  uuid.UUID
	otherWorkspaceID uuid.UUID
	otherOrgID       uuid.UUID
}

func newScopedAuditFixture(t *testing.T) *scopedAuditFixture {
	t.Helper()
	f := newEventFixture(t)
	owner, ok := f.owner.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		t.Fatal("owner identity missing")
	}
	orgID := uuid.MustParse(f.orgID)
	workspaces := make([]uuid.UUID, 0, 2)
	for _, name := range []string{"audit-primary", "audit-other"} {
		id, err := f.queries.CreateWorkspace(
			t.Context(),
			genDb.CreateWorkspaceParams{OrgID: orgID, Name: name, CreatedBy: owner.ID},
		)
		if err != nil {
			t.Fatal(err)
		}
		workspaces = append(workspaces, id)
	}
	resources := make([]uuid.UUID, 0, 2)
	for _, name := range []string{"audit-service", "audit-sibling"} {
		id, err := f.queries.CreateResource(
			t.Context(),
			genDb.CreateResourceParams{
				WorkspaceID: workspaces[0],
				Name:        name,
				Type:        genDb.ResourceTypeService,
				Spec:        []byte(`{}`),
				SpecVersion: 1,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		resources = append(resources, id)
	}
	otherOrg, err := f.queries.CreateOrganization(
		t.Context(),
		genDb.CreateOrganizationParams{Name: "audit-foreign", CreatedBy: owner.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	result := &scopedAuditFixture{f, workspaces[0], resources[0], resources[1], workspaces[1], otherOrg.ID}
	for _, resourceID := range resources {
		for _, kind := range []string{
			events.ResourceUpdated,
			events.DeploymentDeleted,
			events.DomainDeleted,
			events.BuildCanceled,
			events.SecretDeleted,
		} {
			subject := events.SubjectResource
			subjectID := resourceID
			if kind == events.DeploymentDeleted {
				subject = events.SubjectDeployment
				subjectID = uuid.New()
			}
			if kind == events.DomainDeleted {
				subject = events.SubjectDomain
				subjectID = uuid.New()
			}
			if kind == events.BuildCanceled {
				subject = events.SubjectBuild
				subjectID = uuid.New()
			}
			ev := events.Event{Type: kind, ResourceID: new(resourceID), SubjectType: subject, SubjectID: &subjectID}
			if err := events.Record(f.owner, f.queries, ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, workspaceID := range workspaces {
		ev := events.Event{
			Type:        events.WorkspaceUpdated,
			WorkspaceID: new(workspaceID),
			SubjectType: events.SubjectWorkspace,
			SubjectID:   new(workspaceID),
		}
		if err := events.Record(f.owner, f.queries, ev); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func auditScope(t *testing.T, entityType genDb.EntityType, entityID uuid.UUID, scope genDb.Scope) context.Context {
	t.Helper()
	return context.WithValue(
		t.Context(),
		contextkeys.EntityScopesKey,
		[]genDb.EntityScope{{EntityType: entityType, EntityID: entityID, Scope: scope}},
	)
}

func TestListEventsUsesNarrowestScopeAndParents(t *testing.T) {
	f := newScopedAuditFixture(t)
	orgID := uuid.MustParse(f.orgID)
	for _, tc := range []struct {
		name        string
		entityType  genDb.EntityType
		entityID    uuid.UUID
		scope       genDb.Scope
		workspaceID string
		resourceID  string
		code        connect.Code
		count       int
	}{
		{"organization admin", genDb.EntityTypeOrganization, orgID, genDb.ScopeAdmin, "", "", 0, 13},
		{
			"organization reader denied",
			genDb.EntityTypeOrganization,
			orgID,
			genDb.ScopeRead,
			"",
			"",
			connect.CodePermissionDenied,
			0,
		},
		{"workspace admin", genDb.EntityTypeWorkspace, f.workspaceID, genDb.ScopeAdmin, f.workspaceID.String(), "", 0, 11},
		{
			"workspace reader denied",
			genDb.EntityTypeWorkspace,
			f.workspaceID,
			genDb.ScopeRead,
			f.workspaceID.String(),
			"",
			connect.CodePermissionDenied,
			0,
		},
		{"resource reader", genDb.EntityTypeResource, f.resourceID, genDb.ScopeRead, "", f.resourceID.String(), 0, 5},
		{
			"resource admin",
			genDb.EntityTypeResource,
			f.resourceID,
			genDb.ScopeAdmin,
			f.workspaceID.String(),
			f.resourceID.String(),
			0,
			5,
		},
		{
			"resource does not grant workspace",
			genDb.EntityTypeResource,
			f.resourceID,
			genDb.ScopeRead,
			f.workspaceID.String(),
			"",
			connect.CodePermissionDenied,
			0,
		},
		{
			"resource does not grant organization",
			genDb.EntityTypeResource,
			f.resourceID,
			genDb.ScopeRead,
			"",
			"",
			connect.CodePermissionDenied,
			0,
		},
		{
			"workspace parent mismatch",
			genDb.EntityTypeResource,
			f.resourceID,
			genDb.ScopeRead,
			f.otherWorkspaceID.String(),
			f.resourceID.String(),
			connect.CodeNotFound,
			0,
		},
		{
			"sibling resource denied",
			genDb.EntityTypeResource,
			f.resourceID,
			genDb.ScopeRead,
			"",
			f.otherResourceID.String(),
			connect.CodePermissionDenied,
			0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &eventv1.ListEventsRequest{OrgId: f.orgID, WorkspaceId: tc.workspaceID, ResourceId: tc.resourceID}
			resp, err := f.events.ListEvents(auditScope(t, tc.entityType, tc.entityID, tc.scope), req)
			if tc.code != 0 {
				if codeOf(err) != tc.code {
					t.Fatalf("error=%v, want %s", err, tc.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.GetEvents()) != tc.count {
				t.Fatalf("events=%d, want %d", len(resp.GetEvents()), tc.count)
			}
			for _, ev := range resp.GetEvents() {
				if tc.resourceID != "" && ev.GetResourceId() != tc.resourceID {
					t.Fatalf("resource event escaped scope: %v", ev)
				}
				if tc.workspaceID != "" && ev.GetWorkspaceId() != tc.workspaceID {
					t.Fatalf("workspace event escaped scope: %v", ev)
				}
			}
		})
	}
	request := &eventv1.ListEventsRequest{OrgId: f.otherOrgID.String(), ResourceId: f.resourceID.String()}
	if _, err := f.events.ListEvents(
		auditScope(t, genDb.EntityTypeResource, f.resourceID, genDb.ScopeRead),
		request,
	); codeOf(
		err,
	) != connect.CodeNotFound {
		t.Fatalf("organization mismatch=%v", err)
	}
	missing := uuid.New()
	request = &eventv1.ListEventsRequest{OrgId: f.orgID, ResourceId: missing.String()}
	if _, err := f.events.ListEvents(
		auditScope(t, genDb.EntityTypeResource, f.resourceID, genDb.ScopeRead),
		request,
	); codeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("missing unauthorized resource=%v", err)
	}
	if _, err := f.events.ListEvents(t.Context(), request); codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous=%v", err)
	}
}

func TestListEventsChecksWorkspaceAuthorizationBeforeParents(t *testing.T) {
	f := newScopedAuditFixture(t)
	ctx := auditScope(t, genDb.EntityTypeWorkspace, f.workspaceID, genDb.ScopeAdmin)
	wrongOrg := &eventv1.ListEventsRequest{OrgId: f.otherOrgID.String(), WorkspaceId: f.workspaceID.String()}
	if _, err := f.events.ListEvents(ctx, wrongOrg); codeOf(err) != connect.CodeNotFound {
		t.Fatalf("workspace organization mismatch=%v", err)
	}
	missing := uuid.New()
	for _, id := range []uuid.UUID{f.otherWorkspaceID, missing} {
		_, err := f.events.ListEvents(ctx, &eventv1.ListEventsRequest{OrgId: f.orgID, WorkspaceId: id.String()})
		if codeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("unauthorized workspace=%v", err)
		}
	}
	_, err := f.events.ListEvents(auditScope(t, genDb.EntityTypeWorkspace, missing, genDb.ScopeAdmin),
		&eventv1.ListEventsRequest{OrgId: f.orgID, WorkspaceId: missing.String()})
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("authorized missing workspace=%v", err)
	}
	_, err = f.events.ListEvents(auditScope(t, genDb.EntityTypeResource, missing, genDb.ScopeRead),
		&eventv1.ListEventsRequest{OrgId: f.orgID, ResourceId: missing.String()})
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("authorized missing resource=%v", err)
	}
}

func TestListEventsPagesMatchingHistoryAndRetainsDeletedResource(t *testing.T) {
	f := newScopedAuditFixture(t)
	ctx := auditScope(t, genDb.EntityTypeResource, f.resourceID, genDb.ScopeRead)
	request := &eventv1.ListEventsRequest{OrgId: f.orgID, ResourceId: f.resourceID.String(), PageSize: 2,
		Types: []string{events.DomainDeleted, events.DeploymentDeleted, events.BuildCanceled, events.SecretDeleted}}
	var seqs []int64
	var kinds []string
	for {
		response, err := f.events.ListEvents(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range response.GetEvents() {
			if ev.GetResourceId() != f.resourceID.String() || ev.GetActorEmail() != auditOwnerEmail {
				t.Fatalf("event=%v", ev)
			}
			if len(seqs) > 0 && ev.GetSeq() >= seqs[len(seqs)-1] {
				t.Fatal("sequence is not strictly decreasing")
			}
			seqs = append(seqs, ev.GetSeq())
			kinds = append(kinds, ev.GetType())
		}
		if response.GetNextBeforeSeq() == 0 {
			break
		}
		if request.GetBeforeSeq() != 0 {
			t.Fatal("exact final page should have no cursor")
		}
		request.BeforeSeq = response.GetNextBeforeSeq()
		if err := events.Record(
			f.owner,
			f.queries,
			events.Event{
				Type:        events.SecretDeleted,
				ResourceID:  new(f.resourceID),
				SubjectType: events.SubjectResource,
				SubjectID:   new(f.resourceID),
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	expected := []string{events.SecretDeleted, events.BuildCanceled, events.DomainDeleted, events.DeploymentDeleted}
	if !slices.Equal(kinds, expected) {
		t.Fatalf("types=%v, want %v", kinds, expected)
	}
	deleted, err := f.pool.Exec(t.Context(), "DELETE FROM resources WHERE id=$1", f.resourceID)
	if err != nil || deleted.RowsAffected() != 1 {
		t.Fatalf("delete=%v %v", deleted, err)
	}
	request.ResourceId = ""
	request.WorkspaceId = f.workspaceID.String()
	request.BeforeSeq = 0
	request.PageSize = 100
	response, err := f.events.ListEvents(
		auditScope(t, genDb.EntityTypeWorkspace, f.workspaceID, genDb.ScopeAdmin),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	retained := 0
	for _, ev := range response.GetEvents() {
		if ev.GetResourceId() == f.resourceID.String() {
			retained++
		}
	}
	if retained != 5 {
		t.Fatalf("retained matching events=%d, want5", retained)
	}
	request.ResourceId = f.resourceID.String()
	if _, err := f.events.ListEvents(ctx, request); codeOf(err) != connect.CodeNotFound {
		t.Fatalf("deleted resource=%v", err)
	}
}
