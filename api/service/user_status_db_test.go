package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func (a *membershipAPI) countEvents(t *testing.T, eventType string, subject uuid.UUID) int {
	t.Helper()
	var n int
	if err := a.pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM events WHERE type = $1 AND subject_id = $2`, eventType, subject).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func setStatus(
	t *testing.T, caller membershipUser, target membershipUser, status userv1.UserStatus,
) error {
	t.Helper()
	_, err := caller.clients.User.SetUserStatus(t.Context(), &userv1.SetUserStatusRequest{
		Key: &userv1.SetUserStatusRequest_UserId{UserId: target.id.String()}, Status: status,
	})
	return err
}

func TestSetUserStatusSuspendsAndReinstates(t *testing.T) {
	a := newMembershipAPI(t)
	admin := a.signUp(t, "sysadmin")
	if err := a.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID: admin.id, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin,
	}); err != nil {
		t.Fatalf("grant system admin: %v", err)
	}
	target := a.signUp(t, "target")
	ordinary := a.signUp(t, "ordinary")
	suspended, active := userv1.UserStatus_USER_STATUS_SUSPENDED, userv1.UserStatus_USER_STATUS_ACTIVE

	if code := connect.CodeOf(setStatus(t, ordinary, target, suspended)); code != connect.CodePermissionDenied {
		t.Fatalf("ordinary user suspend code = %v, want %v", code, connect.CodePermissionDenied)
	}
	if _, err := target.clients.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{}); err != nil {
		t.Fatalf("target before suspension: %v", err)
	}

	if suspendErr := setStatus(t, admin, target, suspended); suspendErr != nil {
		t.Fatalf("suspend: %v", suspendErr)
	}
	_, err := target.clients.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{})
	if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
		t.Fatalf("suspended target code = %v (%v), want %v", code, err, connect.CodeUnauthenticated)
	}
	if n := a.countEvents(t, events.UserSuspended, target.id); n != 1 {
		t.Fatalf("user.suspended events = %d, want 1", n)
	}

	if reinstateErr := setStatus(t, admin, target, active); reinstateErr != nil {
		t.Fatalf("reinstate: %v", reinstateErr)
	}
	if _, whoErr := target.clients.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{}); whoErr != nil {
		t.Fatalf("reinstated target: %v", whoErr)
	}
	if n := a.countEvents(t, events.UserReinstated, target.id); n != 1 {
		t.Fatalf("user.reinstated events = %d, want 1", n)
	}

	byEmail := &userv1.SetUserStatusRequest{
		Key: &userv1.SetUserStatusRequest_Email{Email: "target@acme.test"}, Status: suspended,
	}
	if _, emailErr := admin.clients.User.SetUserStatus(t.Context(), byEmail); emailErr != nil {
		t.Fatalf("suspend by email: %v", emailErr)
	}
	_, err = target.clients.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("target suspended by email: %v", err)
	}
	byEmail.Key = &userv1.SetUserStatusRequest_Email{Email: "nobody@acme.test"}
	_, err = admin.clients.User.SetUserStatus(t.Context(), byEmail)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("unknown email code = %v, want %v", connect.CodeOf(err), connect.CodeNotFound)
	}

	unknown := membershipUser{id: uuid.New()}
	if code := connect.CodeOf(setStatus(t, admin, unknown, suspended)); code != connect.CodeNotFound {
		t.Fatalf("unknown user code = %v, want %v", code, connect.CodeNotFound)
	}
}

func TestSetUserStatusRejectsInvalidRequests(t *testing.T) {
	a := newMembershipAPI(t)
	admin := a.signUp(t, "sysadmin")
	if err := a.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID: admin.id, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin,
	}); err != nil {
		t.Fatalf("grant system admin: %v", err)
	}
	err := setStatus(t, admin, admin, userv1.UserStatus_USER_STATUS_UNSPECIFIED)
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("unspecified status code = %v (%v), want %v", code, err, connect.CodeInvalidArgument)
	}
	_, err = admin.clients.User.SetUserStatus(t.Context(), &userv1.SetUserStatusRequest{
		Status: userv1.UserStatus_USER_STATUS_SUSPENDED,
	})
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("missing key code = %v (%v), want %v", code, err, connect.CodeInvalidArgument)
	}
}

func TestWhoAmIReturnsClaimedOrgs(t *testing.T) {
	o := newMembershipOrg(t)
	member := o.api.signUp(t, "member")
	scopes, err := authz.New(o.api.pool, o.api.queries).UserScopes(t.Context(), member.id)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	orgID := uuid.MustParse(o.orgID)
	server := NewUserServer(o.api.pool, o.api.queries, o.api.machine, auth.Admins{})
	call := func(claimed []auth.ClaimedOrg) *userv1.WhoAmIResponse {
		entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: member.id}
		ctx := context.WithValue(t.Context(), contextkeys.EntityKey, entity)
		ctx = context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
		ctx = context.WithValue(ctx, contextkeys.ClaimedOrgsKey, claimed)
		resp, whoErr := server.WhoAmI(ctx, &userv1.WhoAmIRequest{})
		if whoErr != nil {
			t.Fatalf("whoami: %v", whoErr)
		}
		return resp
	}

	if got := call(nil).GetClaimedOrgs(); len(got) != 0 {
		t.Fatalf("claimed orgs without a claim = %v", got)
	}
	got := call([]auth.ClaimedOrg{{OrgID: orgID, OrgName: membershipOrgName, InviterName: "Owner"}}).GetClaimedOrgs()
	if len(got) != 1 || got[0].GetOrgId() != o.orgID || got[0].GetOrgName() != membershipOrgName ||
		got[0].GetInviterName() != "Owner" {
		t.Fatalf("claimed orgs = %v", got)
	}
}
