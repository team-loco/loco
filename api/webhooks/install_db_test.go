package webhooks_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/team-loco/loco/api/events"
	"github.com/team-loco/loco/api/webhooks"
)

const (
	installAll      = "https://all.install.test/events"
	installFiltered = "https://filtered.install.test/events"
	installExtra    = "https://extra.install.test/events"
	concurrentSyncs = 4

	checkViolation             = "23514"
	webhookKindOwnerConstraint = "webhooks_kind_owner"
)

type installRow struct {
	id         uuid.UUID
	secret     string
	eventTypes []string
}

func (f *fixture) installRows(t *testing.T) map[string]installRow {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), "SELECT id, url, secret, event_types FROM webhooks WHERE kind = 'install'")
	if err != nil {
		t.Fatalf("install rows: %v", err)
	}
	defer rows.Close()
	out := map[string]installRow{}
	for rows.Next() {
		var url string
		var row installRow
		if scanErr := rows.Scan(&row.id, &url, &row.secret, &row.eventTypes); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		out[url] = row
	}
	if rows.Err() != nil {
		t.Fatalf("install rows: %v", rows.Err())
	}
	return out
}

func (f *fixture) sync(t *testing.T, hooks ...webhooks.InstallWebhook) {
	t.Helper()
	if err := webhooks.SyncInstallWebhooks(t.Context(), f.pool, hooks); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

func (f *fixture) deliveryCount(t *testing.T, webhookID uuid.UUID) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(
		t.Context(),
		"SELECT count(*) FROM webhook_deliveries WHERE webhook_id = $1",
		webhookID,
	).Scan(&n); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	return n
}

func newSecret(t *testing.T) string {
	t.Helper()
	secret, err := webhooks.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	return secret
}

func TestInstallWebhooksReceiveEveryEvent(t *testing.T) {
	f := newFixture(t)
	f.sync(
		t,
		webhooks.InstallWebhook{URL: installAll, Secret: newSecret(t), EventTypes: []string{}},
		webhooks.InstallWebhook{
			URL:        installFiltered,
			Secret:     newSecret(t),
			EventTypes: []string{events.ClusterRegistered},
		},
	)
	workspace, _ := f.webhook(t, f.workspaceID)

	f.record(t, f.workspaceID, events.ResourceCreated)
	f.record(t, f.other, events.EnvironmentCreated)
	if err := events.Record(
		t.Context(),
		f.queries,
		events.Event{Type: events.OrgUpdated, OrgID: &f.orgID},
	); err != nil {
		t.Fatalf("org event: %v", err)
	}
	if err := events.Record(t.Context(), f.queries, events.Event{Type: events.ClusterRegistered}); err != nil {
		t.Fatalf("org-less event: %v", err)
	}

	installed := f.installRows(t)
	typesOf := func(id uuid.UUID) []string {
		rows := f.deliveries(t, id)
		out := make([]string, 0, len(rows))
		for _, d := range rows {
			out = append(out, d.EventType)
		}
		slices.Sort(out)
		return out
	}
	everything := []string{
		events.ClusterRegistered,
		events.EnvironmentCreated,
		events.OrgUpdated,
		events.ResourceCreated,
	}
	slices.Sort(everything)
	if got := typesOf(installed[installAll].id); !slices.Equal(got, everything) {
		t.Fatalf("install-wide webhook got %v, want %v", got, everything)
	}
	if got := typesOf(installed[installFiltered].id); !slices.Equal(got, []string{events.ClusterRegistered}) {
		t.Fatalf("filtered install-wide webhook got %v", got)
	}
	if got := typesOf(workspace.ID); !slices.Equal(got, []string{events.ResourceCreated}) {
		t.Fatalf("workspace webhook got %v", got)
	}
}

func TestSyncInstallWebhooks(t *testing.T) {
	f := newFixture(t)
	first, second := newSecret(t), newSecret(t)
	f.sync(t,
		webhooks.InstallWebhook{URL: installAll, Secret: first, EventTypes: []string{}},
		webhooks.InstallWebhook{URL: installFiltered, Secret: first, EventTypes: []string{events.ResourceUpdated}},
	)
	workspaceHook, _ := f.webhook(t, f.workspaceID)
	f.setURL(t, workspaceHook.ID, installFiltered)
	f.record(t, f.workspaceID, events.ResourceUpdated)
	before := f.installRows(t)
	if len(before) != 2 || f.deliveryCount(t, before[installFiltered].id) != 1 {
		t.Fatalf("after first sync = %+v", before)
	}

	updated := []webhooks.InstallWebhook{
		{URL: installAll, Secret: second, EventTypes: []string{events.ClusterRegistered}},
		{URL: installExtra, Secret: first, EventTypes: []string{}},
	}
	for range 2 {
		f.sync(t, updated...)
		after := f.installRows(t)
		if len(after) != 2 {
			t.Fatalf("after update = %+v", after)
		}
		all := after[installAll]
		if all.id != before[installAll].id || all.secret != second ||
			!slices.Equal(all.eventTypes, []string{events.ClusterRegistered}) {
			t.Fatalf("updated webhook = %+v, before %+v", all, before[installAll])
		}
		if extra, ok := after[installExtra]; !ok || extra.secret != first || len(extra.eventTypes) != 0 {
			t.Fatalf("added webhook = %+v (%v)", extra, ok)
		}
		if f.deliveryCount(t, before[installFiltered].id) != 0 {
			t.Fatal("removed webhook kept its deliveries")
		}
		if f.deliveryCount(t, workspaceHook.ID) != 1 {
			t.Fatal("workspace webhook with a removed install url lost its delivery")
		}
	}

	var wg sync.WaitGroup
	for range concurrentSyncs {
		wg.Go(func() {
			hooks := []webhooks.InstallWebhook{
				{URL: installFiltered, Secret: first, EventTypes: []string{}},
				{URL: installExtra, Secret: second, EventTypes: []string{}},
			}
			if err := webhooks.SyncInstallWebhooks(context.Background(), f.pool, hooks); err != nil {
				t.Errorf("concurrent sync: %v", err)
			}
		})
	}
	wg.Wait()
	concurrent := f.installRows(t)
	if _, ok := concurrent[installAll]; ok || len(concurrent) != 2 || concurrent[installExtra].secret != second {
		t.Fatalf("after concurrent syncs = %+v", concurrent)
	}

	f.sync(t)
	if left := f.installRows(t); len(left) != 0 {
		t.Fatalf("empty config left %+v", left)
	}
	if hooks, err := f.queries.ListWorkspaceWebhooks(t.Context(), f.workspaceID); err != nil || len(hooks) != 1 {
		t.Fatalf("workspace webhooks after sync = %+v (%v)", hooks, err)
	}
}

func TestInstallWebhooksBypassPrivateNetworkGuard(t *testing.T) {
	f := newFixture(t)
	secret := newSecret(t)
	r := newReceiver(t, secret)
	f.sync(t, webhooks.InstallWebhook{URL: r.srv.URL, Secret: secret, EventTypes: []string{}})
	workspace, _ := f.webhook(t, f.workspaceID)
	f.setURL(t, workspace.ID, r.srv.URL)
	f.record(t, f.workspaceID, events.ResourceUpdated)

	d := webhooks.NewDispatcher(f.queries, webhooks.NewClient(false), webhooks.NewClient(true))
	if sent, err := d.DispatchDue(t.Context()); err != nil || sent != 2 {
		t.Fatalf("dispatch = %d, %v", sent, err)
	}
	if got := r.types(); !slices.Equal(got, []string{events.ResourceUpdated}) {
		t.Fatalf("receiver got %v", got)
	}
	install := f.deliveries(t, f.installRows(t)[r.srv.URL].id)[0]
	if install.Status != webhooks.StatusSucceeded || install.LastStatusCode == nil ||
		*install.LastStatusCode != http.StatusNoContent {
		t.Fatalf("install-wide delivery = %+v", install)
	}
	refused := f.deliveries(t, workspace.ID)[0]
	if refused.Status != webhooks.StatusPending || refused.LastError == nil ||
		!strings.Contains(*refused.LastError, webhooks.ErrBlockedAddress.Error()) {
		t.Fatalf("workspace delivery to loopback = %+v", refused)
	}
}

func TestWebhookKindMatchesOwner(t *testing.T) {
	f := newFixture(t)
	secret := newSecret(t)
	for name, tc := range map[string]struct {
		kind      string
		workspace *uuid.UUID
	}{
		"install with a workspace":    {kind: "install", workspace: &f.workspaceID},
		"workspace without workspace": {kind: "workspace"},
	} {
		_, err := f.pool.Exec(
			t.Context(),
			"INSERT INTO webhooks (kind, workspace_id, url, secret) VALUES ($1, $2, $3, $4)",
			tc.kind,
			tc.workspace,
			installExtra,
			secret,
		)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != checkViolation ||
			pgErr.ConstraintName != webhookKindOwnerConstraint {
			t.Fatalf("%s: insert = %v", name, err)
		}
	}
}
