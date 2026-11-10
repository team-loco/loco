package service

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/webhooksig"
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
	provider    secretkeys.Provider
	resolver    *auth.Resolver
	owner       context.Context
	orgID       uuid.UUID
	workspaceID string
	otherID     string
}

func newWebhookFixture(t *testing.T) *webhookFixture {
	t.Helper()
	pool := dbtest.NewPool(t)
	queries := genDb.New(pool)
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := &webhookFixture{
		pool:     pool,
		queries:  queries,
		provider: newTestKeyProvider(t, "k1"),
		resolver: auth.NewResolver(pool, policy),
	}
	owner := f.signUp(t, "owner", "owner@corp.test")
	ownerCtx := scoped(t, queries, owner)
	orgs := NewOrgServer(pool, queries, InvitationConfig{})
	created, err := orgs.CreateOrg(ownerCtx, &orgv1.CreateOrgRequest{Name: new("corp")})
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	f.orgID = uuid.MustParse(created.GetOrgId())
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
	user, _, err := f.resolver.Resolve(t.Context(), auth.Identity{
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
	s := NewWebhookServer(f.pool, f.queries, f.provider, false)

	for _, bad := range []string{"http://hooks.example.test/loco", "https://user:pw@hooks.example.test", "ftp://x.test"} {
		if _, err := s.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{
			WorkspaceId: f.workspaceID, Url: bad,
		}); codeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("url %q accepted: %v", bad, err)
		}
	}

	created, err := s.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL, EventTypes: []string{events.EnvironmentCreated},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(created.GetSecret(), "whsec_") || created.GetWebhook().GetUrl() != receiverURL {
		t.Fatalf("created = %+v", created)
	}
	hookID := created.GetWebhook().GetId()

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
	deliveries, err := s.ListWebhookDeliveries(f.owner, &webhookv1.ListWebhookDeliveriesRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID,
	})
	if err != nil || len(deliveries.GetDeliveries()) != 1 {
		t.Fatalf("deliveries = %+v (%v)", deliveries, err)
	}
	if d := deliveries.GetDeliveries()[0]; d.GetEventType() != events.EnvironmentCreated ||
		d.GetStatus() != webhooks.StatusPending {
		t.Fatalf("delivery = %+v", d)
	}

	listed, err := s.ListWebhooks(
		f.owner,
		&webhookv1.ListWebhooksRequest{WorkspaceId: f.workspaceID},
	)
	if err != nil || len(listed.GetWebhooks()) != 1 {
		t.Fatalf("list = %+v (%v)", listed, err)
	}

	remove := &webhookv1.DeleteWebhookRequest{WorkspaceId: f.workspaceID, WebhookId: hookID}
	if _, deleteErr := s.DeleteWebhook(f.owner, remove); deleteErr != nil {
		t.Fatalf("delete: %v", deleteErr)
	}
	if _, deleteErr := s.DeleteWebhook(f.owner, remove); codeOf(deleteErr) != connect.CodeNotFound {
		t.Fatalf("second delete: %v", deleteErr)
	}

	dbtest.WaitForEarlierTransactions(t, f.pool)
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
	s := NewWebhookServer(f.pool, f.queries, f.provider, false)
	admin := f.member(t, "admin", genDb.ScopeAdmin)
	writer := f.member(t, "writer", genDb.ScopeWrite)

	created, err := s.CreateWebhook(admin, &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL,
	})
	if err != nil {
		t.Fatalf("workspace admin create: %v", err)
	}
	hookID := created.GetWebhook().GetId()
	listed, err := s.ListWebhooks(admin, &webhookv1.ListWebhooksRequest{WorkspaceId: f.workspaceID})
	if err != nil || len(listed.GetWebhooks()) != 1 {
		t.Fatalf("workspace admin list = %+v (%v)", listed, err)
	}

	if _, createErr := s.CreateWebhook(writer, &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL,
	}); codeOf(createErr) != connect.CodePermissionDenied {
		t.Fatalf("writer created a webhook: %v", createErr)
	}
	if _, listErr := s.ListWebhooks(writer, &webhookv1.ListWebhooksRequest{
		WorkspaceId: f.workspaceID,
	}); codeOf(listErr) != connect.CodePermissionDenied {
		t.Fatalf("writer listed webhooks: %v", listErr)
	}
	if _, deleteErr := s.DeleteWebhook(writer, &webhookv1.DeleteWebhookRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID,
	}); codeOf(deleteErr) != connect.CodePermissionDenied {
		t.Fatalf("writer deleted a webhook: %v", deleteErr)
	}
	if _, rotateErr := s.RotateWebhookSecret(writer, &webhookv1.RotateWebhookSecretRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID,
	}); codeOf(rotateErr) != connect.CodePermissionDenied {
		t.Fatalf("writer rotated a webhook secret: %v", rotateErr)
	}
	if _, updateErr := s.UpdateWebhook(writer, &webhookv1.UpdateWebhookRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID, Url: receiverURL,
	}); codeOf(updateErr) != connect.CodePermissionDenied {
		t.Fatalf("writer updated a webhook: %v", updateErr)
	}
	if _, otherErr := s.ListWebhooks(admin, &webhookv1.ListWebhooksRequest{
		WorkspaceId: f.otherID,
	}); codeOf(otherErr) != connect.CodePermissionDenied {
		t.Fatalf("workspace admin listed another workspace's webhooks: %v", otherErr)
	}
	if _, crossErr := s.DeleteWebhook(f.owner, &webhookv1.DeleteWebhookRequest{
		WorkspaceId: f.otherID, WebhookId: hookID,
	}); codeOf(crossErr) != connect.CodeNotFound {
		t.Fatalf("webhook deleted through another workspace: %v", crossErr)
	}

	if _, deleteErr := s.DeleteWebhook(admin, &webhookv1.DeleteWebhookRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID,
	}); deleteErr != nil {
		t.Fatalf("workspace admin delete: %v", deleteErr)
	}
}

func TestWebhookLimitIsPerWorkspace(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, f.provider, false)
	create := &webhookv1.CreateWebhookRequest{WorkspaceId: f.workspaceID, Url: receiverURL}
	for i := range maxWebhooksPerWorkspace {
		if _, err := s.CreateWebhook(f.owner, create); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := s.CreateWebhook(f.owner, create); codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("webhook over the limit: %v", err)
	}
	if _, err := s.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.otherID, Url: receiverURL,
	}); err != nil {
		t.Fatalf("another workspace hit the first workspace's limit: %v", err)
	}

	local := NewWebhookServer(f.pool, f.queries, f.provider, true)
	if _, err := local.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: "http://localhost:3000/hook",
	}); codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("http url with private networks allowed should reach the limit check: %v", err)
	}
}

func TestWebhookChangesFailWithoutTheirEvent(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, f.provider, false)
	if _, err := f.pool.Exec(t.Context(), failEventInsertsSQL); err != nil {
		t.Fatalf("install failing trigger: %v", err)
	}
	if _, err := s.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL,
	}); err == nil {
		t.Fatal("create succeeded without its event")
	}
	hooks, err := f.queries.ListWorkspaceWebhooks(t.Context(), uuid.MustParse(f.workspaceID))
	if err != nil || len(hooks) != 0 {
		t.Fatalf("webhooks after failed create = %+v (%v)", hooks, err)
	}
}

func TestWorkspaceWebhookAPIHidesInstallWebhooks(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, f.provider, false)
	secret, err := webhooksig.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	if syncErr := webhooks.SyncInstallWebhooks(t.Context(), f.pool, f.provider, []webhooks.InstallWebhook{
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
	created, err := s.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	listed, err := s.ListWebhooks(f.owner, &webhookv1.ListWebhooksRequest{
		WorkspaceId: f.workspaceID,
	})
	if err != nil || len(listed.GetWebhooks()) != 1 ||
		listed.GetWebhooks()[0].GetId() != created.GetWebhook().GetId() {
		t.Fatalf("list = %+v (%v)", listed, err)
	}
	if _, deliveriesErr := s.ListWebhookDeliveries(f.owner, &webhookv1.ListWebhookDeliveriesRequest{
		WorkspaceId: f.workspaceID, WebhookId: installID,
	}); codeOf(deliveriesErr) != connect.CodeNotFound {
		t.Fatalf("install webhook deliveries through the workspace API: %v", deliveriesErr)
	}
	if _, deleteErr := s.DeleteWebhook(f.owner, &webhookv1.DeleteWebhookRequest{
		WorkspaceId: f.workspaceID, WebhookId: installID,
	}); codeOf(deleteErr) != connect.CodeNotFound {
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

func TestUpdateWebhookReroutesPendingDeliveriesAndFiltersNewEvents(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, f.provider, false)
	created, err := s.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL, EventTypes: []string{events.EnvironmentCreated},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	hookID := created.GetWebhook().GetId()
	workspaceID := uuid.MustParse(f.workspaceID)
	record := func(eventType string) {
		t.Helper()
		recordErr := events.Record(f.owner, f.queries, events.Event{Type: eventType, WorkspaceID: &workspaceID})
		if recordErr != nil {
			t.Fatalf("record %s: %v", eventType, recordErr)
		}
	}
	record(events.EnvironmentCreated)

	if _, badErr := s.UpdateWebhook(f.owner, &webhookv1.UpdateWebhookRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID, Url: "http://hooks.example.test/loco",
	}); codeOf(badErr) != connect.CodeInvalidArgument {
		t.Fatalf("http url accepted: %v", badErr)
	}
	if _, crossErr := s.UpdateWebhook(f.owner, &webhookv1.UpdateWebhookRequest{
		WorkspaceId: f.otherID, WebhookId: hookID, Url: receiverURL,
	}); codeOf(crossErr) != connect.CodeNotFound {
		t.Fatalf("webhook updated through another workspace: %v", crossErr)
	}

	const movedURL = "https://moved.example.test/loco"
	updated, err := s.UpdateWebhook(f.owner, &webhookv1.UpdateWebhookRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID, Url: movedURL, EventTypes: []string{events.ResourceCreated},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if w := updated.GetWebhook(); w.GetId() != hookID || w.GetUrl() != movedURL ||
		len(w.GetEventTypes()) != 1 || w.GetEventTypes()[0] != events.ResourceCreated {
		t.Fatalf("updated = %+v", w)
	}
	record(events.EnvironmentCreated)
	record(events.ResourceCreated)

	deliveries, err := s.ListWebhookDeliveries(f.owner, &webhookv1.ListWebhookDeliveriesRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID,
	})
	if err != nil {
		t.Fatalf("deliveries: %v", err)
	}
	types := make([]string, 0, len(deliveries.GetDeliveries()))
	for _, d := range deliveries.GetDeliveries() {
		types = append(types, d.GetEventType())
	}
	if len(types) != 2 || types[0] != events.ResourceCreated || types[1] != events.EnvironmentCreated {
		t.Fatalf("delivered event types newest first = %v", types)
	}
	earliest := deliveries.GetDeliveries()[1]
	payload, err := f.queries.GetWebhookDeliveryPayload(t.Context(), uuid.MustParse(earliest.GetId()))
	if err != nil || payload.Url != movedURL {
		t.Fatalf("pending delivery sends to %q (%v), want %q", payload.Url, err, movedURL)
	}

	dbtest.WaitForEarlierTransactions(t, f.pool)
	all, err := f.queries.ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{MaxRows: 100})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var changes int
	for _, e := range all {
		if e.Type == events.WebhookUpdated && e.SubjectID != nil && e.SubjectID.String() == hookID {
			changes++
		}
	}
	if changes != 1 {
		t.Fatalf("%s events = %d, want 1", events.WebhookUpdated, changes)
	}
}

func TestWebhookDeliveriesPageAndFilterByStatus(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, f.provider, false)
	created, err := s.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	hookID := created.GetWebhook().GetId()
	workspaceID := uuid.MustParse(f.workspaceID)
	for range 3 {
		event := events.Event{Type: events.ResourceUpdated, WorkspaceID: &workspaceID}
		if recordErr := events.Record(f.owner, f.queries, event); recordErr != nil {
			t.Fatalf("record: %v", recordErr)
		}
	}
	list := func(t *testing.T, req *webhookv1.ListWebhookDeliveriesRequest) *webhookv1.ListWebhookDeliveriesResponse {
		t.Helper()
		req.WorkspaceId, req.WebhookId = f.workspaceID, hookID
		resp, listErr := s.ListWebhookDeliveries(f.owner, req)
		if listErr != nil {
			t.Fatalf("list: %v", listErr)
		}
		return resp
	}
	all := list(t, &webhookv1.ListWebhookDeliveriesRequest{})
	if len(all.GetDeliveries()) != 4 || all.GetNextPageToken() != "" {
		t.Fatalf("deliveries = %d, next = %q; want the creation event and three updates on one page",
			len(all.GetDeliveries()), all.GetNextPageToken())
	}
	failedID := all.GetDeliveries()[1].GetId()
	if _, execErr := f.pool.Exec(t.Context(), `UPDATE webhook_deliveries
		SET status = 'failed'
		WHERE id = $1`, failedID); execErr != nil {
		t.Fatalf("fail delivery: %v", execErr)
	}

	failed := list(t, &webhookv1.ListWebhookDeliveriesRequest{Status: webhooks.StatusFailed}).GetDeliveries()
	if len(failed) != 1 || failed[0].GetId() != failedID {
		t.Fatalf("failed deliveries = %+v", failed)
	}
	pending := list(t, &webhookv1.ListWebhookDeliveriesRequest{Status: webhooks.StatusPending}).GetDeliveries()
	if len(pending) != 3 {
		t.Fatalf("pending deliveries = %d, want 3", len(pending))
	}

	newestFirst := make([]string, 0, len(all.GetDeliveries()))
	for _, d := range all.GetDeliveries() {
		newestFirst = append(newestFirst, d.GetId())
	}
	page := func(t *testing.T, token string) ([]string, string) {
		t.Helper()
		resp := list(t, &webhookv1.ListWebhookDeliveriesRequest{PageSize: 1, PageToken: token})
		ids := make([]string, 0, len(resp.GetDeliveries()))
		for _, d := range resp.GetDeliveries() {
			ids = append(ids, d.GetId())
		}
		return ids, resp.GetNextPageToken()
	}
	deleteRow := func(t *testing.T, id string) {
		t.Helper()
		if _, execErr := f.pool.Exec(t.Context(), "DELETE FROM webhook_deliveries WHERE id = $1", id); execErr != nil {
			t.Fatal(execErr)
		}
	}
	assertPagingSurvivesDeletingTheCursorRow(t, newestFirst, page, deleteRow)
}

func TestRedeliverWebhookDeliveryRequeuesFinishedDeliveries(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, f.provider, false)
	hook := func(url string) string {
		t.Helper()
		created, err := s.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{WorkspaceId: f.workspaceID, Url: url})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return created.GetWebhook().GetId()
	}
	hookID := hook(receiverURL)
	otherHookID := hook(installURL)
	deliveries, err := s.ListWebhookDeliveries(f.owner, &webhookv1.ListWebhookDeliveriesRequest{
		WorkspaceId: f.workspaceID, WebhookId: hookID,
	})
	if err != nil || len(deliveries.GetDeliveries()) == 0 {
		t.Fatalf("deliveries = %+v (%v)", deliveries, err)
	}
	deliveryID := deliveries.GetDeliveries()[0].GetId()
	redeliver := func(webhookID string) error {
		_, redeliverErr := s.RedeliverWebhookDelivery(f.owner, &webhookv1.RedeliverWebhookDeliveryRequest{
			WorkspaceId: f.workspaceID, WebhookId: webhookID, DeliveryId: deliveryID,
		})
		return redeliverErr
	}

	if redeliverErr := redeliver(hookID); codeOf(redeliverErr) != connect.CodeFailedPrecondition {
		t.Fatalf("pending delivery redelivered: %v", redeliverErr)
	}
	if _, execErr := f.pool.Exec(t.Context(), `UPDATE webhook_deliveries
		SET status = 'failed', attempts = 7, last_error = 'endpoint returned 503',
		    next_attempt_at = NOW() + INTERVAL '1 day'
		WHERE id = $1`, deliveryID); execErr != nil {
		t.Fatalf("fail delivery: %v", execErr)
	}
	if redeliverErr := redeliver(otherHookID); codeOf(redeliverErr) != connect.CodeNotFound {
		t.Fatalf("delivery redelivered through another webhook: %v", redeliverErr)
	}
	if redeliverErr := redeliver(hookID); redeliverErr != nil {
		t.Fatalf("redeliver: %v", redeliverErr)
	}

	var status string
	var attempts int
	var due, cleared bool
	if scanErr := f.pool.QueryRow(t.Context(), `SELECT status, attempts, next_attempt_at <= NOW(),
		    last_error IS NULL
		FROM webhook_deliveries WHERE id = $1`, deliveryID).Scan(&status, &attempts, &due, &cleared); scanErr != nil {
		t.Fatalf("delivery: %v", scanErr)
	}
	if status != webhooks.StatusPending || attempts != 0 || !due || !cleared {
		t.Fatalf("redelivered = %s after %d attempts, due %t, cleared %t", status, attempts, due, cleared)
	}
}

func TestSendTestEventReachesOnlyItsWebhook(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, f.provider, false)
	secret, err := webhooksig.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	if syncErr := webhooks.SyncInstallWebhooks(t.Context(), f.pool, f.provider, []webhooks.InstallWebhook{
		{URL: installURL, Secret: secret, EventTypes: []string{}},
	}); syncErr != nil {
		t.Fatalf("sync: %v", syncErr)
	}
	create := func(types ...string) string {
		t.Helper()
		created, createErr := s.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{
			WorkspaceId: f.workspaceID, Url: receiverURL, EventTypes: types,
		})
		if createErr != nil {
			t.Fatalf("create: %v", createErr)
		}
		return created.GetWebhook().GetId()
	}
	tested := create(events.EnvironmentCreated)
	sibling := create()

	if _, sendErr := s.SendTestEvent(f.owner, &webhookv1.SendTestEventRequest{
		WorkspaceId: f.otherID, WebhookId: tested,
	}); codeOf(sendErr) != connect.CodeNotFound {
		t.Fatalf("test event sent through another workspace: %v", sendErr)
	}
	if _, sendErr := s.SendTestEvent(f.owner, &webhookv1.SendTestEventRequest{
		WorkspaceId: f.workspaceID, WebhookId: tested,
	}); sendErr != nil {
		t.Fatalf("send test event: %v", sendErr)
	}

	var receivers []string
	rows, err := f.pool.Query(t.Context(), `SELECT d.webhook_id::text FROM webhook_deliveries d
		JOIN events e ON e.seq = d.event_seq WHERE e.type = $1`, events.WebhookTest)
	if err != nil {
		t.Fatalf("test deliveries: %v", err)
	}
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		receivers = append(receivers, id)
	}
	if rows.Err() != nil || len(receivers) != 1 || receivers[0] != tested {
		t.Fatalf("test event delivered to %v (%v), want only %s and not %s or the install webhook",
			receivers, rows.Err(), tested, sibling)
	}
}

func TestRotateWebhookSecretKeepsThePreviousSecretForTheGracePeriod(t *testing.T) {
	f := newWebhookFixture(t)
	s := NewWebhookServer(f.pool, f.queries, f.provider, false)
	created, err := s.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID, Url: receiverURL,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	hookID := created.GetWebhook().GetId()
	rotate := func(grace int32) *webhookv1.RotateWebhookSecretResponse {
		t.Helper()
		rotated, rotateErr := s.RotateWebhookSecret(f.owner, &webhookv1.RotateWebhookSecretRequest{
			WorkspaceId: f.workspaceID, WebhookId: hookID, GracePeriodSeconds: grace,
		})
		if rotateErr != nil {
			t.Fatalf("rotate: %v", rotateErr)
		}
		return rotated
	}
	stored := func() genDb.Webhook {
		t.Helper()
		row, getErr := f.queries.GetWorkspaceWebhook(t.Context(), genDb.GetWorkspaceWebhookParams{
			ID: uuid.MustParse(hookID), WorkspaceID: uuid.MustParse(f.workspaceID),
		})
		if getErr != nil {
			t.Fatalf("get: %v", getErr)
		}
		return row
	}

	if _, crossErr := s.RotateWebhookSecret(f.owner, &webhookv1.RotateWebhookSecretRequest{
		WorkspaceId: f.otherID, WebhookId: hookID,
	}); codeOf(crossErr) != connect.CodeNotFound {
		t.Fatalf("secret rotated through another workspace: %v", crossErr)
	}

	const hour = 3600
	graced := rotate(hour)
	if !strings.HasPrefix(graced.GetSecret(), "whsec_") || graced.GetSecret() == created.GetSecret() {
		t.Fatalf("rotated secret = %q", graced.GetSecret())
	}
	row := stored()
	if got := f.open(t, row, row.Secret, row.PreviousSecret); !slices.Equal(
		got, []string{graced.GetSecret(), created.GetSecret()},
	) {
		t.Fatalf("stored secrets open to %q", got)
	}
	expires := graced.GetWebhook().GetPreviousSecretExpiresAt().AsTime()
	if left := time.Until(expires); left <= 0 || left > hour*time.Second {
		t.Fatalf("previous secret expires in %s, want within an hour", left)
	}

	immediate := rotate(0)
	if row = stored(); f.open(t, row, row.Secret)[0] != immediate.GetSecret() || row.PreviousSecret != nil ||
		immediate.GetWebhook().GetPreviousSecretExpiresAt() != nil {
		t.Fatalf("after rotating without a grace period = %+v", row)
	}

	dbtest.WaitForEarlierTransactions(t, f.pool)
	all, err := f.queries.ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{MaxRows: 100})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var rotations int
	for _, e := range all {
		if e.Type == events.WebhookSecretRotated {
			rotations++
		}
	}
	if rotations != 2 {
		t.Fatalf("%s events = %d, want 2", events.WebhookSecretRotated, rotations)
	}
}

func (f *webhookFixture) open(t *testing.T, row genDb.Webhook, sealed ...[]byte) []string {
	t.Helper()
	key := secretkeys.WrappedKey{Provider: row.Provider, KeyID: row.KekID, Bytes: row.WrappedDek}
	secrets, err := webhooks.OpenSecrets(t.Context(), f.provider, row.ID, key, sealed...)
	if err != nil {
		t.Fatalf("open stored secrets: %v", err)
	}
	return secrets
}
