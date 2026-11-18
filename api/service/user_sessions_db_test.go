package service

import (
	"errors"
	"net/netip"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

func TestAccountSessions(t *testing.T) {
	t.Parallel()
	api := newMembershipAPI(t)
	ctx := t.Context()
	owner := api.signUp(t, "session-owner")
	other := api.signUp(t, "session-other")

	const agent = "test dashboard browser"
	identity, err := api.queries.GetIdentity(ctx, genDb.GetIdentityParams{Issuer: testIssuer, Subject: owner.subject})
	if err != nil {
		t.Fatal(err)
	}
	webAccess, webRefresh, err := api.machine.IssueWebSession(ctx, owner.id, identity.ID, netip.Addr{}, agent)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := func(access string) uuid.UUID {
		t.Helper()
		principal, tokenErr := api.machine.GetToken(ctx, access)
		if tokenErr != nil {
			t.Fatalf("read issued session: %v", tokenErr)
		}
		return principal.SessionID
	}
	webID, cliID, foreignID := sessionID(webAccess), sessionID(owner.token), sessionID(other.token)
	expiredAccess, _, err := api.machine.IssueSession(ctx, owner.id, nil, netip.Addr{}, "expired")
	if err != nil {
		t.Fatal(err)
	}
	if _, expireErr := api.pool.Exec(ctx, `UPDATE session_tokens SET access_expires_at = now() - interval '1 day',
		refresh_expires_at = CASE WHEN id = $1 THEN now() - interval '1 day' ELSE refresh_expires_at END
		WHERE id IN ($1, $2)`, sessionID(expiredAccess), webID); expireErr != nil {
		t.Fatal(expireErr)
	}

	listRequest := &userv1.ListSessionsRequest{}
	list, err := owner.clients.User.ListSessions(ctx, listRequest)
	if err != nil {
		t.Fatal(err)
	}
	sessions := list.GetSessions()
	if len(sessions) != 2 || sessions[0].GetId() != webID.String() ||
		sessions[0].GetKind() != userv1.SessionKind_SESSION_KIND_WEB || sessions[0].GetCurrent() ||
		sessions[0].GetUserAgent() != agent || sessions[0].GetCreatedAt() == nil || sessions[0].GetLastUsedAt() == nil ||
		sessions[1].GetId() != cliID.String() || !sessions[1].GetCurrent() ||
		sessions[1].GetKind() != userv1.SessionKind_SESSION_KIND_CLI {
		t.Fatalf("unexpected sessions: %v", sessions)
	}

	ownerEntity := genDb.Entity{Type: genDb.EntityTypeUser, ID: owner.id}
	personal, _ := administrativeToken(t, api, owner, ownerEntity, genDb.EntityScope{
		EntityType: ownerEntity.Type, EntityID: ownerEntity.ID, Scope: genDb.ScopeRead,
	})
	_, listErr := personal.User.ListSessions(ctx, listRequest)
	if connect.CodeOf(listErr) != connect.CodePermissionDenied {
		t.Fatalf("list using personal access token: %v", listErr)
	}
	revoke := &userv1.RevokeSessionRequest{SessionId: webID.String()}
	_, revokeErr := personal.User.RevokeSession(ctx, revoke)
	if connect.CodeOf(revokeErr) != connect.CodePermissionDenied {
		t.Fatalf("revoke using personal access token: %v", revokeErr)
	}
	foreignSession := &userv1.RevokeSessionRequest{SessionId: foreignID.String()}
	if _, foreignErr := owner.clients.User.RevokeSession(ctx, foreignSession); foreignErr != nil {
		t.Fatalf("revoke foreign session: %v", foreignErr)
	}
	if _, whoErr := other.clients.User.WhoAmI(ctx, &userv1.WhoAmIRequest{}); whoErr != nil {
		t.Fatalf("foreign session was revoked: %v", whoErr)
	}
	rotated, newRefresh, err := api.machine.Refresh(ctx, webRefresh, genDb.SessionKindWeb)
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionID(rotated); got != webID {
		t.Fatalf("rotation changed session ID from %s to %s", webID, got)
	}
	for range 2 {
		if _, err := owner.clients.User.RevokeSession(ctx, revoke); err != nil {
			t.Fatalf("revoke own session: %v", err)
		}
	}
	if _, err := api.machine.GetToken(ctx, rotated); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("revoked access credential: %v", err)
	}
	_, _, refreshErr := api.machine.Refresh(ctx, newRefresh, genDb.SessionKindWeb)
	if !errors.Is(refreshErr, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("revoked refresh credential: %v", refreshErr)
	}
}
