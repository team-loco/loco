package service

import (
	"errors"
	"net/http"
	"net/netip"
	"testing"

	"connectrpc.com/connect/v2"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/internal/rpc"
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
	web, err := api.machine.GetToken(ctx, webAccess)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := api.machine.GetToken(ctx, owner.token)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := api.machine.GetToken(ctx, other.token)
	if err != nil {
		t.Fatal(err)
	}
	expiredAccess, _, err := api.machine.IssueSession(ctx, owner.id, nil, netip.Addr{}, "expired")
	if err != nil {
		t.Fatal(err)
	}
	expired, err := api.machine.GetToken(ctx, expiredAccess)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.pool.Exec(ctx, `UPDATE session_tokens SET access_expires_at = now() - interval '1 day',
		refresh_expires_at = CASE WHEN id = $1 THEN now() - interval '1 day' ELSE refresh_expires_at END
		WHERE id IN ($1, $2)`, expired.SessionID, web.SessionID); err != nil {
		t.Fatal(err)
	}

	list, err := owner.clients.User.ListSessions(ctx, &userv1.ListSessionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	sessions := list.GetSessions()
	if len(sessions) != 2 || sessions[0].GetId() != web.SessionID.String() ||
		sessions[0].GetKind() != userv1.SessionKind_SESSION_KIND_WEB || sessions[0].GetCurrent() ||
		sessions[0].GetUserAgent() != agent || sessions[0].GetCreatedAt() == nil || sessions[0].GetLastUsedAt() == nil ||
		sessions[1].GetId() != cli.SessionID.String() || !sessions[1].GetCurrent() ||
		sessions[1].GetKind() != userv1.SessionKind_SESSION_KIND_CLI {
		t.Fatalf("unexpected sessions: %v", sessions)
	}

	issued, err := owner.clients.Token.CreateToken(ctx, &tokenv1.CreateTokenRequest{
		Name: "sessions-test", EntityType: tokenv1.EntityType_ENTITY_TYPE_USER,
		EntityId: owner.id.String(), ExpiresInSec: 3600,
		Scopes: []*tokenv1.EntityScope{{
			EntityType: tokenv1.EntityType_ENTITY_TYPE_USER, EntityId: owner.id.String(), Scope: tokenv1.Scope_SCOPE_READ,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	personal := rpc.NewHTTPClients(http.DefaultClient, api.url, issued.GetToken())
	if _, err := personal.User.ListSessions(ctx, &userv1.ListSessionsRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("list using personal access token: %v", err)
	}
	revoke := &userv1.RevokeSessionRequest{SessionId: web.SessionID.String()}
	if _, err := personal.User.RevokeSession(ctx, revoke); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("revoke using personal access token: %v", err)
	}
	if _, err := owner.clients.User.RevokeSession(ctx, &userv1.RevokeSessionRequest{SessionId: foreign.SessionID.String()}); err != nil {
		t.Fatalf("revoke foreign session: %v", err)
	}
	if _, err := other.clients.User.WhoAmI(ctx, &userv1.WhoAmIRequest{}); err != nil {
		t.Fatalf("foreign session was revoked: %v", err)
	}
	rotated, newRefresh, err := api.machine.Refresh(ctx, webRefresh, genDb.SessionKindWeb)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := owner.clients.User.RevokeSession(ctx, revoke); err != nil {
			t.Fatalf("revoke own session: %v", err)
		}
	}
	if _, err := api.machine.GetToken(ctx, rotated); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("revoked access credential: %v", err)
	}
	if _, _, err := api.machine.Refresh(ctx, newRefresh, genDb.SessionKindWeb); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("revoked refresh credential: %v", err)
	}
}
