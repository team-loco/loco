package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
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
	principal, ok := reqctx.PrincipalFrom(f.owner)
	owner := principal.Entity
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
				CreatedBy:   owner.ID,
				WorkspaceID: workspaces[0],
				Name:        name,
				Slug:        name,
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
	scopes := []genDb.EntityScope{{EntityType: entityType, EntityID: entityID, Scope: scope}}
	return reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: scopes})
}

func TestListEventsUsesNarrowestScopeAndParents(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	f := newScopedAuditFixture(t)
	ctx := auditScope(t, genDb.EntityTypeResource, f.resourceID, genDb.ScopeRead)
	request := &eventv1.ListEventsRequest{OrgId: f.orgID, ResourceId: f.resourceID.String(), PageSize: 2,
		Query: "@type:" + events.DomainDeleted + " OR @type:" + events.DeploymentDeleted + " OR @type:" +
			events.BuildCanceled + " OR @type:" + events.SecretDeleted}
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

func TestDeploymentAndScaleEventsStayInTheirEnvironment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	stagingID := f.deployToProductionAndStaging(t)
	for _, environmentID := range []uuid.UUID{f.envID, stagingID} {
		f.finishRollouts(t)
		if err := scaleResourceIn(t, f, environmentID, 2); err != nil {
			t.Fatal(err)
		}
	}
	for _, environmentID := range []uuid.UUID{f.envID, stagingID} {
		wantEnvironmentEvent(t, f, f.resourceID, environmentID, events.DeploymentCreated)
		wantEnvironmentEvent(t, f, f.resourceID, environmentID, events.ResourceScaled)
	}
}

func TestApplyDeploymentEventsStayInTheirEnvironment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	stagingID := f.prepareStaging(t)
	file := planFileHeader + applyFileWorkerOverride
	for _, environmentID := range []uuid.UUID{f.envID, stagingID} {
		applyOK(t, f, file, applyOptions{environmentID: environmentID})
	}
	resource, found := f.resourceByName(t, applyWorker)
	if !found {
		t.Fatal("worker resource missing")
	}
	for _, environmentID := range []uuid.UUID{f.envID, stagingID} {
		wantEnvironmentEvent(t, f, resource.ID, environmentID, events.DeploymentCreated)
	}
}

func wantEnvironmentEvent(
	t *testing.T,
	f *deployFixture,
	resourceID, environmentID uuid.UUID,
	kind string,
) *eventv1.Event {
	t.Helper()
	server := NewEventServer(f.pool, f.queries)
	owner, err := f.queries.GetWorkspaceOrganizationIDByResourceID(t.Context(), resourceID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auditScope(t, genDb.EntityTypeResource, resourceID, genDb.ScopeRead)
	response, err := server.ListEvents(ctx, &eventv1.ListEventsRequest{
		OrgId: owner.OrgID.String(), ResourceId: resourceID.String(), EnvironmentId: environmentID.String(),
		Query: "@type:" + kind,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetEvents()) != 1 {
		t.Fatalf("%s events in %s = %v, want one", kind, environmentID, response.GetEvents())
	}
	if event := response.GetEvents()[0]; event.GetEnvironmentId() != environmentID.String() {
		t.Fatalf("event environment = %s, want %s", event.GetEnvironmentId(), environmentID)
	}
	return response.GetEvents()[0]
}

func TestWorkspaceEventStreamFiltersReconnectAndRevocation(t *testing.T) {
	t.Parallel()
	f := newScopedAuditFixture(t)
	f.events.poll = time.Millisecond
	user, err := f.queries.CreateUser(t.Context(), genDb.CreateUserParams{Email: "stream@acme.test"})
	if err != nil {
		t.Fatal(err)
	}
	grant := genDb.AddUserScopeParams{
		UserID:     user.ID,
		EntityType: genDb.EntityTypeWorkspace,
		EntityID:   f.workspaceID,
		Scope:      genDb.ScopeRead,
	}
	if err = f.queries.AddUserScope(t.Context(), grant); err != nil {
		t.Fatal(err)
	}
	principal, _ := reqctx.PrincipalFrom(scoped(t, f.queries, user.ID))
	server := connect.NewServer(eventPrincipalInjector{principal}.Interceptor)
	eventv1connect.RegisterEventServiceHandler(server, f.events)
	client := eventv1connect.NewEventServiceClient(connect.NewClient(connectinprocess.New(server)))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request := &eventv1.StreamEventsRequest{
		OrgId:       f.orgID,
		WorkspaceId: f.workspaceID.String(),
		ResourceId:  f.resourceID.String(),
		Query:       "@type:" + events.ResourceUpdated,
	}
	stream, err := client.StreamEvents(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	first, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.GetEvents()) != 1 || first.GetEvents()[0].GetResourceId() != f.resourceID.String() ||
		first.GetEvents()[0].GetType() != events.ResourceUpdated {
		t.Fatalf("batch = %v", first)
	}
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err = events.Record(
		f.owner,
		f.queries,
		events.Event{Type: events.ResourceUpdated, ResourceID: &f.resourceID},
	); err != nil {
		t.Fatal(err)
	}
	request.Cursor = first.GetCursor()
	resumed, err := client.StreamEvents(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	next, err := resumed.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if len(next.GetEvents()) != 1 || next.GetEvents()[0].GetSeq() <= first.GetEvents()[0].GetSeq() {
		t.Fatalf("resumed = %v", next)
	}
	if err = f.queries.RemoveUserScope(t.Context(), genDb.RemoveUserScopeParams(grant)); err != nil {
		t.Fatal(err)
	}
	_, err = resumed.Receive()
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("revoked stream: %v", err)
	}
}

type eventPrincipalInjector struct{ principal reqctx.Principal }

func (i eventPrincipalInjector) Interceptor(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		return next(reqctx.WithPrincipal(ctx, i.principal), spec, stream)
	}
}
