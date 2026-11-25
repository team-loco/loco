package service

import (
	"net/http"
	"net/netip"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"

	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/internal/rpc"
)

// operatorFixture has a system reader, a system admin, an ordinary user and a target with two sessions.
type operatorFixture struct {
	api      *membershipAPI
	reader   membershipUser
	admin    membershipUser
	ordinary membershipUser
	target   membershipUser
	webToken string
}

func newOperatorFixture(t *testing.T) operatorFixture {
	t.Helper()
	a := newMembershipAPI(t)
	f := operatorFixture{api: a}
	f.reader = a.signUp(t, "reader")
	a.grantSystem(t, f.reader, genDb.ScopeRead)
	f.admin = a.signUp(t, "admin")
	a.grantSystem(t, f.admin, genDb.ScopeAdmin)
	f.ordinary = a.signUp(t, "ordinary")
	f.target = a.signUp(t, "target")
	var err error
	f.webToken, _, err = a.machine.IssueWebSession(
		t.Context(),
		f.target.id,
		netip.MustParseAddr("198.51.100.9"),
		"browser",
	)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f operatorFixture) sessions(t *testing.T, caller membershipUser) []*userv1.Session {
	t.Helper()
	listed, err := caller.clients.User.ListSessions(
		t.Context(),
		&userv1.ListSessionsRequest{UserId: f.target.id.String()},
	)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	return listed.GetSessions()
}

func wantOperatorCode(t *testing.T, name string, err error, want connect.Code) {
	t.Helper()
	if code := connect.CodeOf(err); code != want {
		t.Errorf("%s: code %v (%v), want %v", name, code, err, want)
	}
}

func TestSystemReaderListsSessionsButChangesNothing(t *testing.T) {
	t.Parallel()
	f := newOperatorFixture(t)
	ctx := t.Context()
	userID := f.target.id.String()

	sessions := f.sessions(t, f.reader)
	if len(sessions) != 2 {
		t.Fatalf("reader sees %d sessions, want 2", len(sessions))
	}
	for _, session := range sessions {
		if session.GetCurrent() {
			t.Errorf("session %s is marked current for the reader", session.GetId())
		}
	}
	for _, caller := range []membershipUser{f.reader, f.ordinary} {
		_, err := caller.clients.User.RevokeSession(ctx,
			&userv1.RevokeSessionRequest{SessionId: sessions[0].GetId(), UserId: userID})
		wantOperatorCode(t, "revoke one session", err, connect.CodePermissionDenied)
		_, err = caller.clients.User.RevokeUserSessions(ctx, &userv1.RevokeUserSessionsRequest{UserId: userID})
		wantOperatorCode(t, "revoke all sessions", err, connect.CodePermissionDenied)
	}
	_, err := f.target.clients.User.RevokeUserSessions(ctx, &userv1.RevokeUserSessionsRequest{UserId: userID})
	wantOperatorCode(t, "revoke all of one's own sessions", err, connect.CodePermissionDenied)
	_, err = f.ordinary.clients.User.ListSessions(ctx, &userv1.ListSessionsRequest{UserId: userID})
	wantOperatorCode(t, "ordinary list", err, connect.CodePermissionDenied)
	if got := len(f.sessions(t, f.admin)); got != 2 {
		t.Fatalf("sessions after denied changes = %d, want 2", got)
	}
}

func TestSystemAdminRevokesOneThenAllSessions(t *testing.T) {
	t.Parallel()
	f := newOperatorFixture(t)
	ctx := t.Context()
	userID := f.target.id.String()

	var webID string
	for _, session := range f.sessions(t, f.admin) {
		if session.GetKind() == userv1.SessionKind_SESSION_KIND_WEB {
			webID = session.GetId()
		}
	}
	if _, err := f.admin.clients.User.RevokeSession(ctx,
		&userv1.RevokeSessionRequest{SessionId: webID, UserId: userID}); err != nil {
		t.Fatalf("revoke one: %v", err)
	}
	revoked := rpc.NewHTTPClients(http.DefaultClient, f.api.url, f.webToken)
	_, err := revoked.User.WhoAmI(ctx, &userv1.WhoAmIRequest{})
	wantOperatorCode(t, "revoked session", err, connect.CodeUnauthenticated)
	remaining := f.sessions(t, f.admin)
	if len(remaining) != 1 || remaining[0].GetId() == webID {
		t.Fatalf("sessions after revoking one = %v", remaining)
	}
	if _, err = f.target.clients.User.WhoAmI(ctx, &userv1.WhoAmIRequest{}); err != nil {
		t.Fatalf("target's other session after revoking one: %v", err)
	}

	if _, err = f.admin.clients.User.RevokeUserSessions(
		ctx,
		&userv1.RevokeUserSessionsRequest{UserId: userID},
	); err != nil {
		t.Fatalf("revoke all: %v", err)
	}
	if got := f.sessions(t, f.admin); len(got) != 0 {
		t.Fatalf("sessions after revoking all = %v", got)
	}
	_, err = f.target.clients.User.WhoAmI(ctx, &userv1.WhoAmIRequest{})
	wantOperatorCode(t, "target after revoking all", err, connect.CodeUnauthenticated)
}

func TestOperatorsPreviewAndSystemAdminDeletesAnotherAccount(t *testing.T) {
	t.Parallel()
	f := newOperatorFixture(t)
	ctx := t.Context()
	userID := f.target.id.String()
	org, err := f.target.clients.Org.CreateOrg(ctx, &orgv1.CreateOrgRequest{Name: new("target-only")})
	if err != nil {
		t.Fatal(err)
	}

	for _, caller := range []membershipUser{f.reader, f.admin} {
		impact, impactErr := caller.clients.User.GetAccountDeletionImpact(ctx,
			&userv1.GetAccountDeletionImpactRequest{UserId: userID})
		if impactErr != nil {
			t.Fatalf("preview: %v", impactErr)
		}
		blocking := impact.GetLastActiveAdminOrganizations()
		if len(blocking) != 1 || blocking[0].GetOrgId() != org.GetOrgId() {
			t.Errorf("blocking organizations = %v, want %s", blocking, org.GetOrgId())
		}
	}
	_, err = f.ordinary.clients.User.GetAccountDeletionImpact(
		ctx,
		&userv1.GetAccountDeletionImpactRequest{UserId: userID},
	)
	wantOperatorCode(t, "ordinary preview", err, connect.CodePermissionDenied)
	_, err = f.admin.clients.User.GetAccountDeletionImpact(ctx,
		&userv1.GetAccountDeletionImpactRequest{UserId: "00000000-0000-4000-8000-000000000000"})
	wantOperatorCode(t, "preview of a missing user", err, connect.CodeNotFound)

	del := &userv1.DeleteUserRequest{UserId: userID}
	for name, caller := range map[string]membershipUser{"reader": f.reader, "ordinary": f.ordinary} {
		_, err = caller.clients.User.DeleteUser(ctx, del)
		wantOperatorCode(t, name+" delete", err, connect.CodePermissionDenied)
	}
	_, err = f.admin.clients.User.DeleteUser(ctx, del)
	wantOperatorCode(t, "delete of the only administrator", err, connect.CodeFailedPrecondition)

	if _, err = f.admin.clients.Org.DeleteOrg(ctx, &orgv1.DeleteOrgRequest{OrgId: org.GetOrgId()}); err != nil {
		t.Fatalf("delete organization: %v", err)
	}
	if _, err = f.admin.clients.User.DeleteUser(ctx, del); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	_, err = f.reader.clients.User.GetUser(
		ctx,
		&userv1.GetUserRequest{Key: &userv1.GetUserRequest_UserId{UserId: userID}},
	)
	wantOperatorCode(t, "get deleted user", err, connect.CodeNotFound)
}

func TestListUserOrgsShowsAnotherUsersScopesToSystemReaders(t *testing.T) {
	t.Parallel()
	f := newOperatorFixture(t)
	ctx := t.Context()
	if _, err := f.target.clients.Org.CreateOrg(ctx, &orgv1.CreateOrgRequest{Name: new("target-org")}); err != nil {
		t.Fatal(err)
	}
	request := &orgv1.ListUserOrgsRequest{UserId: f.target.id.String()}

	listed, err := f.reader.clients.Org.ListUserOrgs(ctx, request)
	if err != nil {
		t.Fatalf("list as reader: %v", err)
	}
	if len(listed.GetOrgs()) != 1 || listed.GetOrgs()[0].GetName() != "target-org" {
		t.Fatalf("orgs = %v", listed.GetOrgs())
	}
	scopes := listed.GetOrgs()[0].GetScopes()
	if !slices.Contains(scopes, "admin") {
		t.Errorf("scopes = %v, want the creator's admin scope", scopes)
	}
	_, err = f.ordinary.clients.Org.ListUserOrgs(ctx, request)
	wantOperatorCode(t, "ordinary list of another user's organizations", err, connect.CodePermissionDenied)
}
