package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/webhooks"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	webhookv1 "github.com/team-loco/loco/gen/go/loco/webhook/v1"
)

const (
	receiverURL = "https://hooks.example.test/loco"
	installURL  = "https://install.example.test/loco"
)

type webhookFixture struct {
	pool        *pgxpool.Pool
	queries     *genDb.Queries
	resolver    *auth.Resolver
	owner       context.Context
	orgID       uuid.UUID
	workspaceID string
	otherID     string
}

func newWebhookFixture(t *testing.T) *webhookFixture {
	t.Helper()
	pool := authtest.NewPool(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Minute,
	})
	t.Cleanup(machine.Close)
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := &webhookFixture{pool: pool, queries: queries, resolver: auth.NewResolver(pool, policy)}
	owner := f.signUp(t, "owner", "owner@corp.test")
	ownerCtx := scoped(t, queries, owner)
	orgs := NewOrgServer(pool, queries, machine)
	created, err := orgs.CreateOrg(ownerCtx, connect.NewRequest(&orgv1.CreateOrgRequest{Name: new("corp")}))
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	f.orgID = uuid.MustParse(created.Msg.GetOrgId())
	f.workspaceID = f.workspace(t, owner, "storefront")
	f.otherID = f.workspace(t, owner, "billing")
	f.owner = scoped(t, queries, owner)
	return f
}

func (f *webhookFixture) workspace(t *testing.T, owner uuid.UUID, name string) string {
	t.Helper()
	id, err := f.queries.CreateWorkspace(t.Context(), genDb.CreateWorkspaceParams{
		OrgID: f.orgID, Name: name, CreatedBy: owner,
	})
	if err != nil {
		t.Fatalf("workspace %s: %v", name, err)
	}
	return id.String()
}

func (f *webhookFixture) signUp(t *testing.T, sub, email string) uuid.UUID {
	t.Helper()
	user, err := f.resolver.Resolve(t.Context(), auth.Identity{
		Issuer: testIssuer, Subject: sub, Email: email, EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("sign up %s: %v", email, err)
	}
	return user.ID
}

func (f *webhookFixture) member(t *testing.T, sub string, scope genDb.Scope) context.Context {
	t.Helper()
	user := f.signUp(t, sub, sub+"@corp.test")
	if err := f.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID:     user,
		EntityType: genDb.EntityTypeWorkspace,
		EntityID:   uuid.MustParse(f.workspaceID),
		Scope:      scope,
	}); err != nil {
		t.Fatalf("grant %s: %v", sub, err)
	}
	return scoped(t, f.queries, user)
}

func TestWebhookLifecycle(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, false)

	for _, bad := range []string{"http://hooks.example.test/loco", "https://user:pw@hooks.example.test", "ftp://x.test"} {
		if _, err := s.CreateWebhook(f.owner, connect.NewRequest(&webhookv1.CreateWebhookRequest{
			WorkspaceId: f.workspaceID, Url: bad,
		})); codeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("url %q accepted: %v", bad, err)
		}
	}

	created, err := s.CreateWebhook(f.owner, connect.NewRequest(&webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL, EventTypes: []string{events.EnvironmentCreated},
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(created.Msg.GetSecret(), "whsec_") || created.Msg.GetWebhook().GetUrl() != receiverURL {
		t.Fatalf("created = %+v", created.Msg)
	}
	hookID := created.Msg.GetWebhook().GetId()

	workspaceID := uuid.MustParse(f.workspaceID)
	for _, eventType := range []string{events.EnvironmentCreated, events.ResourceCreated} {
		if recordErr := events.Record(
			f.owner,
			f.queries,
			events.Event{Type: eventType, WorkspaceID: &workspaceID},
		); recordErr != nil {
			t.Fatalf("record %s: %v", eventType, recordErr)
		}
	}
	deliveries, err := s.ListWebhookDeliveries(f.owner, connect.NewRequest(&webhookv1.ListWebhookDeliveriesRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID,
	}))
	if err != nil || len(deliveries.Msg.GetDeliveries()) != 1 {
		t.Fatalf("deliveries = %+v (%v)", deliveries, err)
	}
	if d := deliveries.Msg.GetDeliveries()[0]; d.GetEventType() != events.EnvironmentCreated ||
		d.GetStatus() != "pending" {
		t.Fatalf("delivery = %+v", d)
	}

	listed, err := s.ListWebhooks(
		f.owner,
		connect.NewRequest(&webhookv1.ListWebhooksRequest{WorkspaceId: f.workspaceID}),
	)
	if err != nil || len(listed.Msg.GetWebhooks()) != 1 {
		t.Fatalf("list = %+v (%v)", listed, err)
	}

	remove := connect.NewRequest(&webhookv1.DeleteWebhookRequest{WorkspaceId: f.workspaceID, WebhookId: hookID})
	if _, deleteErr := s.DeleteWebhook(f.owner, remove); deleteErr != nil {
		t.Fatalf("delete: %v", deleteErr)
	}
	if _, deleteErr := s.DeleteWebhook(f.owner, remove); codeOf(deleteErr) != connect.CodeNotFound {
		t.Fatalf("second delete: %v", deleteErr)
	}

	authtest.WaitForEarlierTransactions(t, f.pool)
	all, err := f.queries.ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{MaxRows: 100})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var recorded []genDb.Event
	for _, e := range all {
		if strings.HasPrefix(e.Type, "webhook.") {
			recorded = append(recorded, e)
		}
	}
	if len(recorded) != 2 || recorded[0].Type != events.WebhookCreated || recorded[1].Type != events.WebhookDeleted {
		t.Fatalf("webhook events = %+v", recorded)
	}
	for _, e := range recorded {
		if e.WorkspaceID == nil || *e.WorkspaceID != workspaceID || e.OrgID == nil || *e.OrgID != f.orgID {
			t.Fatalf("%s recorded workspace %v org %v", e.Type, e.WorkspaceID, e.OrgID)
		}
	}
}

func TestWorkspaceWebhooksRequireWorkspaceAdmin(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, false)
	admin := f.member(t, "admin", genDb.ScopeAdmin)
	writer := f.member(t, "writer", genDb.ScopeWrite)

	created, err := s.CreateWebhook(admin, connect.NewRequest(&webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL,
	}))
	if err != nil {
		t.Fatalf("workspace admin create: %v", err)
	}
	hookID := created.Msg.GetWebhook().GetId()
	listed, err := s.ListWebhooks(admin, connect.NewRequest(&webhookv1.ListWebhooksRequest{WorkspaceId: f.workspaceID}))
	if err != nil || len(listed.Msg.GetWebhooks()) != 1 {
		t.Fatalf("workspace admin list = %+v (%v)", listed, err)
	}

	if _, createErr := s.CreateWebhook(writer, connect.NewRequest(&webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL,
	})); codeOf(createErr) != connect.CodePermissionDenied {
		t.Fatalf("writer created a webhook: %v", createErr)
	}
	if _, listErr := s.ListWebhooks(writer, connect.NewRequest(&webhookv1.ListWebhooksRequest{
		WorkspaceId: f.workspaceID,
	})); codeOf(listErr) != connect.CodePermissionDenied {
		t.Fatalf("writer listed webhooks: %v", listErr)
	}
	if _, deleteErr := s.DeleteWebhook(writer, connect.NewRequest(&webhookv1.DeleteWebhookRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID,
	})); codeOf(deleteErr) != connect.CodePermissionDenied {
		t.Fatalf("writer deleted a webhook: %v", deleteErr)
	}
	if _, otherErr := s.ListWebhooks(admin, connect.NewRequest(&webhookv1.ListWebhooksRequest{
		WorkspaceId: f.otherID,
	})); codeOf(otherErr) != connect.CodePermissionDenied {
		t.Fatalf("workspace admin listed another workspace's webhooks: %v", otherErr)
	}
	if _, crossErr := s.DeleteWebhook(f.owner, connect.NewRequest(&webhookv1.DeleteWebhookRequest{
		WorkspaceId: f.otherID, WebhookId: hookID,
	})); codeOf(crossErr) != connect.CodeNotFound {
		t.Fatalf("webhook deleted through another workspace: %v", crossErr)
	}

	if _, deleteErr := s.DeleteWebhook(admin, connect.NewRequest(&webhookv1.DeleteWebhookRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID,
	})); deleteErr != nil {
		t.Fatalf("workspace admin delete: %v", deleteErr)
	}
}

func TestWebhookLimitIsPerWorkspace(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, false)
	create := connect.NewRequest(&webhookv1.CreateWebhookRequest{WorkspaceId: f.workspaceID, Url: receiverURL})
	for i := range maxWebhooksPerWorkspace {
		if _, err := s.CreateWebhook(f.owner, create); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := s.CreateWebhook(f.owner, create); codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("webhook over the limit: %v", err)
	}
	if _, err := s.CreateWebhook(f.owner, connect.NewRequest(&webhookv1.CreateWebhookRequest{
		WorkspaceId: f.otherID, Url: receiverURL,
	})); err != nil {
		t.Fatalf("another workspace hit the first workspace's limit: %v", err)
	}

	local := NewWebhookServer(f.pool, f.queries, true)
	if _, err := local.CreateWebhook(f.owner, connect.NewRequest(&webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: "http://localhost:3000/hook",
	})); codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("http url with private networks allowed should reach the limit check: %v", err)
	}
}

func TestWebhookChangesFailWithoutTheirEvent(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, false)
	if _, err := f.pool.Exec(t.Context(), failEventInsertsSQL); err != nil {
		t.Fatalf("install failing trigger: %v", err)
	}
	if _, err := s.CreateWebhook(f.owner, connect.NewRequest(&webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL,
	})); err == nil {
		t.Fatal("create succeeded without its event")
	}
	hooks, err := f.queries.ListWorkspaceWebhooks(t.Context(), uuid.MustParse(f.workspaceID))
	if err != nil || len(hooks) != 0 {
		t.Fatalf("webhooks after failed create = %+v (%v)", hooks, err)
	}
}

func TestWorkspaceWebhookAPIHidesInstallWebhooks(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, false)
	secret, err := webhooks.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	if syncErr := webhooks.SyncInstallWebhooks(t.Context(), f.pool, []webhooks.InstallWebhook{
		{URL: installURL, Secret: secret, EventTypes: []string{}},
	}); syncErr != nil {
		t.Fatalf("sync: %v", syncErr)
	}
	var installID string
	if scanErr := f.pool.QueryRow(
		t.Context(),
		"SELECT id::text FROM webhooks WHERE kind = 'install'",
	).Scan(&installID); scanErr != nil {
		t.Fatalf("install webhook: %v", scanErr)
	}
	created, err := s.CreateWebhook(f.owner, connect.NewRequest(&webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL,
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	listed, err := s.ListWebhooks(f.owner, connect.NewRequest(&webhookv1.ListWebhooksRequest{
		WorkspaceId: f.workspaceID,
	}))
	if err != nil || len(listed.Msg.GetWebhooks()) != 1 ||
		listed.Msg.GetWebhooks()[0].GetId() != created.Msg.GetWebhook().GetId() {
		t.Fatalf("list = %+v (%v)", listed, err)
	}
	if _, deliveriesErr := s.ListWebhookDeliveries(f.owner, connect.NewRequest(&webhookv1.ListWebhookDeliveriesRequest{
		WorkspaceId: f.workspaceID, WebhookId: installID,
	})); codeOf(deliveriesErr) != connect.CodeNotFound {
		t.Fatalf("install webhook deliveries through the workspace API: %v", deliveriesErr)
	}
	if _, deleteErr := s.DeleteWebhook(f.owner, connect.NewRequest(&webhookv1.DeleteWebhookRequest{
		WorkspaceId: f.workspaceID, WebhookId: installID,
	})); codeOf(deleteErr) != connect.CodeNotFound {
		t.Fatalf("install webhook deleted through the workspace API: %v", deleteErr)
	}
	var remaining int
	if countErr := f.pool.QueryRow(
		t.Context(),
		"SELECT count(*) FROM webhooks WHERE kind = 'install'",
	).Scan(&remaining); countErr != nil || remaining != 1 {
		t.Fatalf("install webhooks after workspace delete = %d (%v)", remaining, countErr)
	}
	count, err := f.queries.CountWorkspaceWebhooks(t.Context(), uuid.MustParse(f.workspaceID))
	if err != nil || count != 1 {
		t.Fatalf("workspace webhook count = %d (%v)", count, err)
	}
}
