package service

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const operatorReason = "unpaid invoice"

// operatorContext is a request from a user who holds only the installation role.
func operatorContext(t *testing.T, role genDb.Scope) context.Context {
	t.Helper()
	id := uuid.New()
	return reqctx.WithPrincipal(t.Context(), reqctx.Principal{
		Entity:    genDb.Entity{Type: genDb.EntityTypeUser, ID: id},
		Scopes:    []genDb.EntityScope{{EntityType: genDb.EntityTypeSystem, Scope: role}},
		UserID:    id,
		ActorType: genDb.DeploymentActorTypeUser,
		ActorID:   id,
		Method:    genDb.DeploymentMethodWeb,
	})
}

// withInstallationRole adds the installation role to the principal of ctx.
func withInstallationRole(ctx context.Context, role genDb.Scope) context.Context {
	p, _ := reqctx.PrincipalFrom(ctx)
	p.Scopes = append(slices.Clip(p.Scopes), genDb.EntityScope{
		EntityType: genDb.EntityTypeSystem, Scope: role,
	})
	return reqctx.WithPrincipal(ctx, p)
}

func listOrgEvents(
	t *testing.T, f *eventFixture, ctx context.Context, req *eventv1.ListEventsRequest,
) []*eventv1.Event {
	t.Helper()
	resp, err := f.events.ListEvents(ctx, req)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	return resp.GetEvents()
}

func eventOfType(list []*eventv1.Event, eventType string) *eventv1.Event {
	for _, e := range list {
		if e.GetType() == eventType {
			return e
		}
	}
	return nil
}

func TestOperatorChangeIsMarkedInTheOrganizationsLog(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	orgs := NewOrgServer(f.pool, f.queries, InvitationConfig{}, limits.Defaults())
	if _, err := orgs.SuspendOrg(
		operatorContext(t, genDb.ScopeAdmin),
		&orgv1.SuspendOrgRequest{OrgId: f.orgID, Reason: operatorReason},
	); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	list := listOrgEvents(t, f, f.owner, &eventv1.ListEventsRequest{OrgId: f.orgID})
	suspended := eventOfType(list, events.OrgSuspended)
	if suspended == nil || !suspended.GetActedAsOperator() {
		t.Fatalf("org.suspended = %v, want one marked as an operator's", suspended)
	}
	if created := eventOfType(list, events.OrgCreated); created == nil || created.GetActedAsOperator() {
		t.Fatalf("org.created = %v, want one unmarked", created)
	}
}

func TestMemberChangeIsNotMarked(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	orgs := NewOrgServer(f.pool, f.queries, InvitationConfig{}, limits.Defaults())
	for name, ctx := range map[string]context.Context{
		"owner":                    f.owner,
		"owner who is an operator": withInstallationRole(f.owner, genDb.ScopeAdmin),
	} {
		if _, err := orgs.UpdateOrg(ctx, &orgv1.UpdateOrgRequest{OrgId: f.orgID, Name: new(name)}); err != nil {
			t.Fatalf("%s: update org: %v", name, err)
		}
	}
	updated := 0
	for _, e := range listOrgEvents(t, f, f.owner, &eventv1.ListEventsRequest{OrgId: f.orgID}) {
		if e.GetType() == events.OrgUpdated {
			updated++
			if e.GetActedAsOperator() {
				t.Fatalf("org.updated by a member is marked: %v", e)
			}
		}
	}
	if updated != 2 {
		t.Fatalf("org.updated events = %d, want 2", updated)
	}
}

func TestRequestMixingAnInstallationCheckAndAMemberChangeIsNotMarked(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	ctx := withInstallationRole(f.owner, genDb.ScopeAdmin)
	if err := authorizeSystem(ctx, authz.New(f.queries), actions.ListUsers); err != nil {
		t.Fatalf("installation check: %v", err)
	}
	orgs := NewOrgServer(f.pool, f.queries, InvitationConfig{}, limits.Defaults())
	if _, err := orgs.UpdateOrg(ctx, &orgv1.UpdateOrgRequest{OrgId: f.orgID, Name: new("renamed")}); err != nil {
		t.Fatalf("update org: %v", err)
	}
	updated := eventOfType(listOrgEvents(t, f, f.owner, &eventv1.ListEventsRequest{OrgId: f.orgID}), events.OrgUpdated)
	if updated == nil || updated.GetActedAsOperator() {
		t.Fatalf("org.updated = %v, want one unmarked", updated)
	}
}

func TestTokenActorsFollowTheirUser(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	orgs := NewOrgServer(f.pool, f.queries, InvitationConfig{}, limits.Defaults())
	asToken := func(ctx context.Context) context.Context {
		p, _ := reqctx.PrincipalFrom(ctx)
		p.ActorType, p.ActorID, p.Method = genDb.DeploymentActorTypeApiToken, uuid.New(), genDb.DeploymentMethodApiToken
		return reqctx.WithPrincipal(ctx, p)
	}
	rename := &orgv1.UpdateOrgRequest{OrgId: f.orgID, Name: new("by-member")}
	if _, err := orgs.UpdateOrg(asToken(f.owner), rename); err != nil {
		t.Fatalf("member token: %v", err)
	}
	if _, err := orgs.SuspendOrg(
		asToken(operatorContext(t, genDb.ScopeAdmin)), &orgv1.SuspendOrgRequest{OrgId: f.orgID, Reason: operatorReason},
	); err != nil {
		t.Fatalf("operator token: %v", err)
	}
	list := listOrgEvents(t, f, f.owner, &eventv1.ListEventsRequest{OrgId: f.orgID})
	if e := eventOfType(list, events.OrgUpdated); e == nil || e.GetActedAsOperator() {
		t.Fatalf("org.updated by a member's token = %v, want one unmarked", e)
	}
	if e := eventOfType(list, events.OrgSuspended); e == nil || !e.GetActedAsOperator() {
		t.Fatalf("org.suspended by an operator's token = %v, want one marked", e)
	}
}

func TestClusterChangesAreRecordedWithoutAnOrganization(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	ctx := operatorContext(t, genDb.ScopeAdmin)
	agent := testAgentConfig()
	server := NewClusterAdminServer(f.pool, f.queries, agent.JoinTokenTTL, agent.JoinTokenMaxTTL)
	created, err := server.CreateCluster(ctx, &clusterv1.CreateClusterRequest{
		RequestId: uuid.NewString(), Name: "edge", Target: inRegion("local-1"),
	})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	id := created.GetClusterId()
	if _, err = server.IssueJoinToken(ctx, &clusterv1.IssueJoinTokenRequest{ClusterId: id}); err != nil {
		t.Fatalf("issue join token: %v", err)
	}
	steps := []func() error{
		func() error {
			_, e := server.PauseCluster(ctx, &clusterv1.PauseClusterRequest{ClusterId: id})
			return e
		},
		func() error {
			_, e := server.ResumeCluster(ctx, &clusterv1.ResumeClusterRequest{ClusterId: id})
			return e
		},
		func() error {
			_, e := server.PauseCluster(ctx, &clusterv1.PauseClusterRequest{ClusterId: id})
			return e
		},
		func() error {
			_, e := server.RemoveCluster(ctx, &clusterv1.RemoveClusterRequest{ClusterId: id})
			return e
		},
		func() error {
			_, e := server.RestoreCluster(ctx, &clusterv1.RestoreClusterRequest{ClusterId: id})
			return e
		},
	}
	for i, step := range steps {
		if err = step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	want := []string{
		events.ClusterRestored, events.ClusterRemoved, events.ClusterPaused, events.ClusterResumed,
		events.ClusterPaused, events.ClusterTokenIssued, events.ClusterTokenIssued, events.ClusterCreated,
	}
	var got []string
	reader := operatorContext(t, genDb.ScopeRead)
	listed := listOrgEvents(t, f, reader, &eventv1.ListEventsRequest{InstallationOnly: true})
	for _, e := range listed {
		if e.GetOrgId() != "" || e.GetActedAsOperator() {
			t.Fatalf("installation event %v carries an organization or an operator mark", e)
		}
		if e.GetSubjectId() == id {
			got = append(got, e.GetType())
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("cluster events = %v, want %v", got, want)
	}
}

func TestSystemReaderListsEveryOrganizationAndTheInstallation(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	other := seedTenant(t, f.pool, f.queries, "other@acme.test", "other")
	if err := events.Record(t.Context(), f.queries, events.Event{
		Type: events.ClusterPaused, ActorType: events.ActorSystem, SubjectType: events.SubjectCluster,
		SubjectID: new(uuid.New()),
	}); err != nil {
		t.Fatal(err)
	}
	reader := operatorContext(t, genDb.ScopeRead)
	all := dbtest.Events(t, f.pool)
	orgs := map[string]bool{}
	for _, e := range listOrgEvents(t, f, reader, &eventv1.ListEventsRequest{}) {
		orgs[e.GetOrgId()] = true
	}
	for _, id := range []string{f.orgID, other.orgID.String(), ""} {
		if !orgs[id] {
			t.Errorf("listing without an organization misses org %q: %v", id, orgs)
		}
	}
	var wantOther []string
	for _, e := range slices.Backward(all) {
		if e.OrgID != nil && *e.OrgID == other.orgID {
			wantOther = append(wantOther, e.Type)
		}
	}
	page := listOrgEvents(t, f, reader, &eventv1.ListEventsRequest{OrgId: other.orgID.String()})
	if !slices.Equal(eventTypes(page), wantOther) || len(wantOther) == 0 {
		t.Errorf("organization filter returned %v, want %v", eventTypes(page), wantOther)
	}
	installation := listOrgEvents(t, f, reader, &eventv1.ListEventsRequest{InstallationOnly: true})
	if len(installation) == 0 {
		t.Fatal("installation-only listing is empty")
	}
	for _, e := range installation {
		if e.GetOrgId() != "" {
			t.Errorf("installation-only listing returned %v", e)
		}
	}
	paged, err := f.events.ListEvents(reader, &eventv1.ListEventsRequest{
		PageSize: 1, IncludeTotalCount: true, Query: "@type:org.created",
	})
	if err != nil {
		t.Fatal(err)
	}
	if paged.GetTotalCount() != 2 || len(paged.GetEvents()) != 1 || paged.GetNextPageToken() == "" {
		t.Fatalf("paged = %v", paged)
	}
	next, err := f.events.ListEvents(reader, &eventv1.ListEventsRequest{
		PageSize: 1, Query: "@type:org.created", PageToken: paged.GetNextPageToken(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.GetEvents()) != 1 || next.GetEvents()[0].GetSeq() >= paged.GetEvents()[0].GetSeq() ||
		next.GetNextPageToken() != "" {
		t.Fatalf("next page = %v after %v", next, paged)
	}
}

func TestMemberCannotListInstallationEvents(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	if err := events.Record(t.Context(), f.queries, events.Event{
		Type: events.ClusterPaused, ActorType: events.ActorSystem, SubjectType: events.SubjectCluster,
		SubjectID: new(uuid.New()),
	}); err != nil {
		t.Fatal(err)
	}
	for _, e := range listOrgEvents(t, f, f.owner, &eventv1.ListEventsRequest{OrgId: f.orgID}) {
		if e.GetOrgId() == "" {
			t.Fatalf("a member sees the installation event %v", e)
		}
	}
	for name, req := range map[string]*eventv1.ListEventsRequest{
		"every organization": {},
		"installation only":  {InstallationOnly: true},
	} {
		_, err := f.events.ListEvents(f.owner, req)
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s: code = %v, want PermissionDenied", name, connect.CodeOf(err))
		}
	}
	_, err := f.events.ListEvents(operatorContext(t, genDb.ScopeRead), &eventv1.ListEventsRequest{
		OrgId: f.orgID, InstallationOnly: true,
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("installation_only with org_id: code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestOwnerDeletingTheirOrganizationIsNotMarked(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	orgs := NewOrgServer(f.pool, f.queries, InvitationConfig{}, limits.Defaults())
	if _, err := orgs.DeleteOrg(f.owner, &orgv1.DeleteOrgRequest{OrgId: f.orgID}); err != nil {
		t.Fatalf("delete org: %v", err)
	}
	var deleted []genDb.Event
	for _, e := range dbtest.Events(t, f.pool) {
		if e.Type == events.OrgDeleted {
			deleted = append(deleted, e)
		}
	}
	if len(deleted) != 1 || deleted[0].ActedAsOperator {
		t.Fatalf("org.deleted events = %+v, want one unmarked", deleted)
	}
}

// recordAs records an organization event for the request's user, or for actor when one is given.
func recordAs(
	t *testing.T, ctx context.Context, f *eventFixture, orgID uuid.UUID, actor *uuid.UUID,
) genDb.Event {
	t.Helper()
	event := events.Event{Type: events.OrgUpdated, OrgID: &orgID, SubjectType: events.SubjectOrg, SubjectID: &orgID}
	if actor != nil {
		event.ActorType, event.ActorID = string(genDb.DeploymentActorTypeUser), actor
	}
	if err := events.Record(ctx, f.queries, event); err != nil {
		t.Fatal(err)
	}
	all := dbtest.Events(t, f.pool)
	return all[len(all)-1]
}

func TestEventMarkFollowsTheActingUsersRolesInTheEventsOrganization(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	orgID := uuid.MustParse(f.orgID)
	workspaceID := f.tenant.workspace(t, "web")
	resourceID := f.tenant.resource(t, workspaceID, "svc", &resourcev1.ServiceSpec{})
	resolver := openResolver(t, f.pool)
	userWith := func(name string, entityType genDb.EntityType, entityID uuid.UUID) uuid.UUID {
		t.Helper()
		id := signUp(t, resolver, name, name+"@acme.test")
		if entityID != uuid.Nil {
			err := f.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
				UserID: id, EntityType: entityType, EntityID: entityID, Scope: genDb.ScopeRead,
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	workspaceOnly := userWith("workspace-only", genDb.EntityTypeWorkspace, workspaceID)
	resourceOnly := userWith("resource-only", genDb.EntityTypeResource, resourceID)
	noRole := userWith("no-role", "", uuid.Nil)

	for name, tc := range map[string]struct {
		user   uuid.UUID
		marked bool
	}{
		"owner":          {f.tenant.ownerID, false},
		"workspace only": {workspaceOnly, false},
		"resource only":  {resourceOnly, false},
		"no role":        {noRole, true},
	} {
		asRequest := recordAs(t, scoped(t, f.queries, tc.user), f, orgID, nil)
		asActor := recordAs(t, t.Context(), f, orgID, &tc.user)
		if asRequest.ActedAsOperator != tc.marked || asActor.ActedAsOperator != tc.marked {
			t.Errorf("%s: marked = %v as the request's user, %v as an explicit actor; want %v",
				name, asRequest.ActedAsOperator, asActor.ActedAsOperator, tc.marked)
		}
	}
}

func TestOneRequestRecordingInTwoOrganizationsMarksEachByItsMembership(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	other := seedTenant(t, f.pool, f.queries, "other@acme.test", "other")
	if a := recordAs(t, f.owner, f, uuid.MustParse(f.orgID), nil); a.ActedAsOperator {
		t.Errorf("event in the owner's organization is marked: %+v", a)
	}
	if b := recordAs(t, f.owner, f, other.orgID, nil); !b.ActedAsOperator {
		t.Errorf("event in an organization the user is not in is unmarked: %+v", b)
	}
}
