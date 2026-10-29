//go:build conformance

package auth_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/validate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/mail"
	"github.com/team-loco/loco/api/mail/mailtest"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/webhooks"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	dexIssuerURL   = "http://localhost:25556/dex"
	dexTokenURL    = dexIssuerURL + "/token"
	dexClientToken = "loco-conformance"

	journeyOrgName = "Acme"
	journeyWebURL  = "https://app.loco.test"
	journeyFrom    = "Loco <noreply@loco.test>"
	journeyTTL     = time.Hour

	carolEmail = "carol@dex.test"
	carolPass  = "carolpw"
	daveEmail  = "dave@dex.test"
	davePass   = "davepw"
	erinEmail  = "erin@dex.test"
	erinPass   = "erinpw"

	adminSubject = "bootstrap-admin"
	adminEmail   = "admin@loco.test"
	adminIssuer  = "https://bootstrap.loco.test"

	signupModeClosed = "closed"
	signupModeOpen   = "open"

	rejectedClosedSignup = "Sign-ups are closed"
)

type journeyAPI struct {
	pool    *pgxpool.Pool
	queries *genDb.Queries
	url     string
	admin   *rpc.Clients
	adminID uuid.UUID
}

func newJourneyAPI(t *testing.T, mode string, transport mail.Transport) *journeyAPI {
	t.Helper()
	pool := authtest.NewPool(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Hour,
	})
	t.Cleanup(machine.Close)

	issuers, err := auth.ParseIssuers(`[{"issuer":"` + dexIssuerURL + `","audience":"` + conformanceClientID + `"}]`)
	if err != nil {
		t.Fatalf("AUTH_ISSUERS: %v", err)
	}
	policy, err := auth.ParseSignupPolicy(mode, "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	verifier := auth.NewVerifier(http.DefaultClient, issuers)
	resolver := auth.NewResolver(pool, policy)
	authorizer := authz.New(pool, queries)

	server := connect.NewServer(
		interceptor.NewErrorInterceptor(),
		interceptor.NewContextInterceptor().Interceptor(),
		interceptor.NewAuthInterceptor(machine, verifier, resolver, authorizer).Interceptor(),
		validate.NewServerInterceptor(),
	)
	invitations := service.InvitationConfig{Transport: transport, TTL: journeyTTL, WebURL: journeyWebURL}
	orgv1connect.RegisterOrgServiceHandler(server, service.NewOrgServer(pool, queries, machine, invitations))
	userv1connect.RegisterUserServiceHandler(server, service.NewUserServer(pool, queries, machine, auth.Admins{}))
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	openPolicy, err := auth.ParseSignupPolicy(signupModeOpen, "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	admin, _, err := auth.NewResolver(pool, openPolicy).Resolve(t.Context(), auth.Identity{
		Issuer: adminIssuer, Subject: adminSubject, Email: adminEmail, EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	if scopeErr := queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID: admin.ID, Scope: genDb.ScopeAdmin, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil,
	}); scopeErr != nil {
		t.Fatalf("grant system admin: %v", scopeErr)
	}
	session, _, err := machine.IssueSession(t.Context(), admin.ID, nil, "", "journey")
	if err != nil {
		t.Fatalf("admin session: %v", err)
	}
	return &journeyAPI{
		pool:    pool,
		queries: queries,
		url:     httpServer.URL,
		admin:   rpc.NewHTTPClients(http.DefaultClient, httpServer.URL, session),
		adminID: admin.ID,
	}
}

func (a *journeyAPI) existingAccount(t *testing.T, email string) {
	t.Helper()
	openPolicy, err := auth.ParseSignupPolicy(signupModeOpen, "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if _, _, err = auth.NewResolver(a.pool, openPolicy).Resolve(t.Context(), auth.Identity{
		Issuer: adminIssuer, Subject: email, Email: email, EmailVerified: true,
	}); err != nil {
		t.Fatalf("existing account for %s: %v", email, err)
	}
}

func (a *journeyAPI) dexClients(t *testing.T, email, password string) *rpc.Clients {
	t.Helper()
	token := passwordGrant(dexTokenURL, dexClientToken, email, password)(t)
	return rpc.NewHTTPClients(http.DefaultClient, a.url, token)
}

func requireRPCError(t *testing.T, err error, code connect.Code, messagePart string) {
	t.Helper()
	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		t.Fatalf("error = %v, want a connect error", err)
	}
	if connectErr.Code() != code || !strings.Contains(connectErr.Message(), messagePart) {
		t.Fatalf("error = %s %q, want %s containing %q", connectErr.Code(), connectErr.Message(), code, messagePart)
	}
}

func wantLink(email string) string {
	return journeyWebURL + "/login?login_hint=" + strings.ReplaceAll(email, "@", "%40")
}

type sentInvitations struct {
	orgID string
	carol *orgv1.Invitation
	dave  *orgv1.Invitation
}

type deliveryCase struct {
	name       string
	wantStatus orgv1.InvitationStatus
	setup      func(t *testing.T) (mail.Transport, func(t *testing.T, sent sentInvitations))
}

func smtpDelivery(t *testing.T) (mail.Transport, func(t *testing.T, sent sentInvitations)) {
	t.Helper()
	server := mailtest.NewSMTPServer(t, true)
	transport, err := mail.ParseTransport(
		server.URL(mailtest.Password), journeyFrom, "", mail.WithTLSConfig(server.ClientTLS()),
	)
	if err != nil {
		t.Fatalf("smtp transport: %v", err)
	}
	return transport, func(t *testing.T, _ sentInvitations) {
		t.Helper()
		var toCarol []mailtest.Capture
		for range 2 {
			select {
			case got := <-server.Captured:
				if strings.Contains(got.Rcpt, carolEmail) {
					toCarol = append(toCarol, got)
				}
			case <-time.After(time.Second):
				t.Fatal("expected one message per invitation")
			}
		}
		if len(toCarol) != 1 {
			t.Fatalf("messages to %s = %d, want 1", carolEmail, len(toCarol))
		}
		got := toCarol[0]
		for _, want := range []string{
			"To: <" + carolEmail + ">\r\n",
			"Reply-To: <" + adminEmail + ">\r\n",
			wantLink(carolEmail),
			journeyOrgName,
		} {
			if !strings.Contains(got.Data, want) {
				t.Errorf("message to carol lacks %q:\n%s", want, got.Data)
			}
		}
		select {
		case extra := <-server.Captured:
			t.Fatalf("unexpected extra message to %s", extra.Rcpt)
		default:
		}
		t.Logf("smtp sink received exactly one message to %s with link %s", carolEmail, wantLink(carolEmail))
	}
}

type hookCall struct {
	header http.Header
	body   []byte
}

func hookDelivery(t *testing.T) (mail.Transport, func(t *testing.T, sent sentInvitations)) {
	t.Helper()
	secret, err := webhooks.NewSecret()
	if err != nil {
		t.Fatalf("hook secret: %v", err)
	}
	var mu sync.Mutex
	var calls []hookCall
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read hook body: %v", readErr)
		}
		mu.Lock()
		calls = append(calls, hookCall{header: r.Header.Clone(), body: body})
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	config := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	transport, err := mail.ParseTransport(server.URL, journeyFrom, secret, mail.WithTLSConfig(config))
	if err != nil {
		t.Fatalf("hook transport: %v", err)
	}
	return transport, func(t *testing.T, sent sentInvitations) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if len(calls) != 2 {
			t.Fatalf("hook calls = %d, want one per invitation", len(calls))
		}
		var toCarol []hookCall
		for _, call := range calls {
			var event mail.InvitationEvent
			if decodeErr := json.Unmarshal(call.body, &event); decodeErr != nil {
				t.Fatalf("decode hook body: %v", decodeErr)
			}
			if event.Invitation.Email == carolEmail {
				toCarol = append(toCarol, call)
			}
		}
		if len(toCarol) != 1 {
			t.Fatalf("hook calls for %s = %d, want 1", carolEmail, len(toCarol))
		}
		call := toCarol[0]
		id := call.header.Get("Webhook-Id")
		stamp, err := strconv.ParseInt(call.header.Get("Webhook-Timestamp"), 10, 64)
		if err != nil {
			t.Fatalf("webhook-timestamp: %v", err)
		}
		wantSignature, err := webhooks.Sign(secret, id, time.Unix(stamp, 0), call.body)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		if got := call.header.Get("Webhook-Signature"); got != wantSignature {
			t.Fatalf("webhook-signature = %q, want %q", got, wantSignature)
		}
		var event mail.InvitationEvent
		if err := json.Unmarshal(call.body, &event); err != nil {
			t.Fatalf("decode hook body: %v", err)
		}
		if event.Invitation.ID.String() != sent.carol.GetId() ||
			event.Invitation.OrgID.String() != sent.orgID ||
			event.Org.ID.String() != sent.orgID || event.Org.Name != journeyOrgName ||
			event.Inviter.ID != uuid.MustParse(sent.carol.GetInvitedBy().GetId()) ||
			event.Inviter.Email != adminEmail ||
			event.Link != wantLink(carolEmail) {
			t.Fatalf("hook event = %+v", event)
		}
		t.Logf("hook received one signed event for %s (webhook-id %s), signature verified, link %s",
			carolEmail, id, event.Link)
	}
}

func noneDelivery(t *testing.T) (mail.Transport, func(t *testing.T, sent sentInvitations)) {
	t.Helper()
	transport, err := mail.ParseTransport("", "", "")
	if err != nil {
		t.Fatalf("empty transport: %v", err)
	}
	return transport, func(t *testing.T, sent sentInvitations) {
		t.Helper()
		for _, invitation := range []*orgv1.Invitation{sent.carol, sent.dave} {
			if invitation.GetLink() != wantLink(invitation.GetEmail()) {
				t.Fatalf("link = %q, want %q", invitation.GetLink(), wantLink(invitation.GetEmail()))
			}
		}
		t.Logf("no transport: status NO_TRANSPORT with link %s", sent.carol.GetLink())
	}
}

func TestInvitationsJourney(t *testing.T) {
	cases := []deliveryCase{
		{name: "smtp", wantStatus: orgv1.InvitationStatus_INVITATION_STATUS_EMAILED, setup: smtpDelivery},
		{name: "https hook", wantStatus: orgv1.InvitationStatus_INVITATION_STATUS_EMAILED, setup: hookDelivery},
		{name: "no transport", wantStatus: orgv1.InvitationStatus_INVITATION_STATUS_NO_TRANSPORT, setup: noneDelivery},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport, checkDelivery := tc.setup(t)
			api := newJourneyAPI(t, signupModeClosed, transport)
			api.existingAccount(t, carolEmail)
			api.existingAccount(t, daveEmail)
			ctx := t.Context()

			org, err := api.admin.Org.CreateOrg(ctx, &orgv1.CreateOrgRequest{Name: new(journeyOrgName)})
			if err != nil {
				t.Fatalf("step 1: create org: %v", err)
			}
			orgID := org.GetOrgId()
			t.Logf("step 1: admin created org %s (%s)", journeyOrgName, orgID)

			var sent sentInvitations
			sent.orgID = orgID
			for _, target := range []struct {
				email string
				into  **orgv1.Invitation
			}{{carolEmail, &sent.carol}, {daveEmail, &sent.dave}} {
				invited, inviteErr := api.admin.Org.InviteOrgMember(ctx, &orgv1.InviteOrgMemberRequest{
					OrgId: orgID, Email: target.email,
				})
				if inviteErr != nil {
					t.Fatalf("step 2: invite %s: %v", target.email, inviteErr)
				}
				invitation := invited.GetInvitation()
				if invitation.GetStatus() != tc.wantStatus || invitation.GetLink() != wantLink(target.email) {
					t.Fatalf("step 2: invitation for %s = %+v, want status %s", target.email, invitation, tc.wantStatus)
				}
				*target.into = invitation
			}
			t.Logf("step 2: admin invited %s and %s, status %s", carolEmail, daveEmail, tc.wantStatus)
			checkDelivery(t, sent)

			if _, revokeErr := api.admin.Org.RevokeInvitation(ctx, &orgv1.RevokeInvitationRequest{
				Id: sent.dave.GetId(),
			}); revokeErr != nil {
				t.Fatalf("step 3: revoke: %v", revokeErr)
			}
			t.Logf("step 3: admin revoked the invitation for %s", daveEmail)

			api.signInSteps(t, orgID, sent.carol.GetId())
		})
	}
}

func TestInvitationsOpenSignup(t *testing.T) {
	noTransport, err := mail.ParseTransport("", "", "")
	if err != nil {
		t.Fatalf("empty transport: %v", err)
	}
	api := newJourneyAPI(t, signupModeOpen, noTransport)
	erin := api.dexClients(t, erinEmail, erinPass)
	whoami, err := erin.User.WhoAmI(t.Context(), &userv1.WhoAmIRequest{})
	if err != nil {
		t.Fatalf("step 6 (open): erin whoami: %v", err)
	}
	if len(whoami.GetClaimedOrgs()) != 0 {
		t.Fatalf("step 6 (open): claimed orgs = %v", whoami.GetClaimedOrgs())
	}
	orgs, err := erin.Org.ListUserOrgs(t.Context(), &orgv1.ListUserOrgsRequest{UserId: whoami.GetUser().GetId()})
	if err != nil {
		t.Fatalf("step 6 (open): erin list orgs: %v", err)
	}
	if len(orgs.GetOrgs()) != 0 {
		t.Fatalf("step 6 (open): erin orgs = %v, want none", orgs.GetOrgs())
	}
	t.Logf("step 6 (open mode): erin created with no orgs")
}

func (a *journeyAPI) signInSteps(t *testing.T, orgID, carolInvitationID string) {
	t.Helper()
	ctx := t.Context()
	carol := a.dexClients(t, carolEmail, carolPass)
	whoami, err := carol.User.WhoAmI(ctx, &userv1.WhoAmIRequest{})
	if err != nil {
		t.Fatalf("step 4: carol whoami: %v", err)
	}
	carolID := whoami.GetUser().GetId()
	claimed := whoami.GetClaimedOrgs()
	if len(claimed) != 1 || claimed[0].GetOrgId() != orgID || claimed[0].GetOrgName() != journeyOrgName {
		t.Fatalf("step 4: claimed orgs = %v", claimed)
	}
	orgs, err := carol.Org.ListUserOrgs(ctx, &orgv1.ListUserOrgsRequest{UserId: carolID})
	if err != nil {
		t.Fatalf("step 4: carol list orgs: %v", err)
	}
	if len(orgs.GetOrgs()) != 1 || orgs.GetOrgs()[0].GetId() != orgID {
		t.Fatalf("step 4: carol orgs = %v", orgs.GetOrgs())
	}
	open, err := a.admin.Org.ListInvitations(ctx, &orgv1.ListInvitationsRequest{OrgId: orgID})
	if err != nil {
		t.Fatalf("step 4: list invitations: %v", err)
	}
	if len(open.GetInvitations()) != 0 {
		t.Fatalf("step 4: open invitations = %v, want none after claim and revoke", open.GetInvitations())
	}
	row, err := a.queries.GetInvitation(ctx, uuid.MustParse(carolInvitationID))
	if err != nil {
		t.Fatalf("step 4: load invitation: %v", err)
	}
	if row.ClaimedAt == nil || row.ClaimedBy == nil || row.ClaimedBy.String() != carolID {
		t.Fatalf("step 4: invitation claimed_by = %v claimed_at = %v", row.ClaimedBy, row.ClaimedAt)
	}
	t.Logf("step 4: carol signed in to her account via Dex, claimed %s, ListUserOrgs = [%s], invitation claimed_by her",
		journeyOrgName, journeyOrgName)

	dave := a.dexClients(t, daveEmail, davePass)
	daveWhoami, err := dave.User.WhoAmI(ctx, &userv1.WhoAmIRequest{})
	if err != nil {
		t.Fatalf("step 5: dave whoami: %v", err)
	}
	if len(daveWhoami.GetClaimedOrgs()) != 0 {
		t.Fatalf("step 5: dave claimed orgs = %v, want none from a revoked invitation", daveWhoami.GetClaimedOrgs())
	}
	daveOrgs, err := dave.Org.ListUserOrgs(ctx, &orgv1.ListUserOrgsRequest{UserId: daveWhoami.GetUser().GetId()})
	if err != nil {
		t.Fatalf("step 5: dave list orgs: %v", err)
	}
	if len(daveOrgs.GetOrgs()) != 0 {
		t.Fatalf("step 5: dave orgs = %v, want none", daveOrgs.GetOrgs())
	}
	t.Logf("step 5: dave signed in to his existing account via Dex, revoked invitation not claimed")

	erin := a.dexClients(t, erinEmail, erinPass)
	_, err = erin.User.WhoAmI(ctx, &userv1.WhoAmIRequest{})
	requireRPCError(t, err, connect.CodePermissionDenied, rejectedClosedSignup)
	if _, lookupErr := a.queries.GetUserByEmail(ctx, erinEmail); lookupErr == nil {
		t.Fatal("step 6: erin was provisioned")
	}
	t.Logf("step 6: erin rejected in closed mode, no user created")

	setStatus := func(status userv1.UserStatus) error {
		_, setErr := a.admin.User.SetUserStatus(ctx, &userv1.SetUserStatusRequest{
			Key: &userv1.SetUserStatusRequest_Email{Email: carolEmail}, Status: status,
		})
		return setErr
	}
	if err = setStatus(userv1.UserStatus_USER_STATUS_SUSPENDED); err != nil {
		t.Fatalf("step 7: suspend: %v", err)
	}
	_, err = carol.User.WhoAmI(ctx, &userv1.WhoAmIRequest{})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("step 7: suspended carol call = %v, want unauthenticated", err)
	}
	if err = setStatus(userv1.UserStatus_USER_STATUS_ACTIVE); err != nil {
		t.Fatalf("step 7: reinstate: %v", err)
	}
	if _, err = carol.User.WhoAmI(ctx, &userv1.WhoAmIRequest{}); err != nil {
		t.Fatalf("step 7: reinstated carol call: %v", err)
	}
	t.Logf("step 7: suspended carol rejected (unauthenticated), reinstated carol served")

	if _, err = a.admin.Org.RemoveOrgMember(ctx, &orgv1.RemoveOrgMemberRequest{
		OrgId: orgID, UserId: carolID,
	}); err != nil {
		t.Fatalf("step 8: remove member: %v", err)
	}
	again, err := carol.User.WhoAmI(ctx, &userv1.WhoAmIRequest{})
	if err != nil {
		t.Fatalf("step 8: carol after removal: %v", err)
	}
	if len(again.GetClaimedOrgs()) != 0 {
		t.Fatalf("step 8: claimed orgs after removal = %v", again.GetClaimedOrgs())
	}
	orgs, err = carol.Org.ListUserOrgs(ctx, &orgv1.ListUserOrgsRequest{UserId: carolID})
	if err != nil {
		t.Fatalf("step 8: carol list orgs: %v", err)
	}
	if len(orgs.GetOrgs()) != 0 {
		t.Fatalf("step 8: carol orgs after removal = %v, want none", orgs.GetOrgs())
	}
	t.Logf("step 8: admin removed carol, her claimed invitation did not re-add her")
}
