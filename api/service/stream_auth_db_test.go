package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/pgnotify"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/api/tvm"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/rpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type streamCredential uint8

const (
	streamSession streamCredential = iota
	streamPersonal
	streamOwned
)

type streamChange uint8

const (
	streamDelete streamChange = iota
	streamSuspend
	streamExpireSession
	streamRemoveGrants
	streamRevoke
	streamExpireToken
	streamDatabaseUnavailable
)

func TestIdleEventStreamsObserveCredentialAndPermissionChanges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		credential streamCredential
		system     bool
		change     streamChange
		want       connect.Code
	}{
		{"session deleted", streamSession, false, streamDelete, connect.CodeUnauthenticated},
		{"session suspended", streamSession, false, streamSuspend, connect.CodeUnauthenticated},
		{"session expired", streamSession, false, streamExpireSession, connect.CodeUnauthenticated},
		{"session grant removed", streamSession, false, streamRemoveGrants, connect.CodePermissionDenied},
		{"system session grant removed", streamSession, true, streamRemoveGrants, connect.CodePermissionDenied},
		{"personal deleted", streamPersonal, false, streamDelete, connect.CodeUnauthenticated},
		{"personal suspended", streamPersonal, false, streamSuspend, connect.CodeUnauthenticated},
		{"personal revoked", streamPersonal, false, streamRevoke, connect.CodeUnauthenticated},
		{"personal grant removed", streamPersonal, false, streamRemoveGrants, connect.CodePermissionDenied},
		{"system personal revoked", streamPersonal, true, streamRevoke, connect.CodeUnauthenticated},
		{"owned revoked", streamOwned, false, streamRevoke, connect.CodeUnauthenticated},
		{"owned expired", streamOwned, false, streamExpireToken, connect.CodeUnauthenticated},
		{"database unavailable", streamSession, false, streamDatabaseUnavailable, connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, userID, workspaceID := newStreamCredentialFixture(t)
			var scopes []genDb.EntityScope
			if tc.system {
				scopes = []genDb.EntityScope{
					{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin},
				}
				if err := f.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
					UserID: userID, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin,
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				scopes = []genDb.EntityScope{
					{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
				}
			}
			token := issueStreamCredential(t, f, userID, workspaceID, tc.credential, scopes)
			clients := streamCredentialClients(t, f, token)
			request := &eventv1.StreamEventsRequest{}
			if !tc.system {
				request.OrgId, request.WorkspaceId = f.orgID, workspaceID.String()
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			stream, err := clients.Event.StreamEvents(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if _, err = stream.Receive(); err != nil {
				t.Fatalf("initial event batch: %v", err)
			}
			// The mutation uses a separate DB operation after opening the HTTP stream.
			switch tc.change {
			case streamDatabaseUnavailable:
				f.pool.Close()
			case streamDelete:
				err = f.queries.DeleteUser(t.Context(), userID)
			case streamSuspend:
				_, err = f.queries.SetUserStatus(t.Context(), genDb.SetUserStatusParams{
					ID: userID, Status: genDb.UserStatusSuspended,
				})
			case streamRemoveGrants:
				_, err = f.pool.Exec(t.Context(), "DELETE FROM user_scopes WHERE user_id = $1", userID)
			case streamRevoke:
				err = f.machine.Revoke(t.Context(), token)
			case streamExpireSession:
				_, err = f.pool.Exec(
					t.Context(),
					"UPDATE session_tokens SET access_expires_at = NOW() - interval '1 second' WHERE user_id = $1",
					userID,
				)
			case streamExpireToken:
				_, err = f.pool.Exec(
					t.Context(),
					"UPDATE api_tokens SET expires_at = NOW() - interval '1 second' WHERE token_hash = $1",
					bearer.Hash(token),
				)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = stream.Receive(); connect.CodeOf(err) != tc.want {
				t.Fatalf("idle stream after %s = %v, want %s", tc.name, err, tc.want)
			}
		})
	}
}

func TestIdleDashboardStreamClosesAfterSuspension(t *testing.T) {
	t.Parallel()
	f, userID, workspaceID := newStreamCredentialFixture(t)
	identities, err := f.queries.ListIdentitiesForUser(t.Context(), userID)
	if err != nil || len(identities) != 1 {
		t.Fatalf("fixture identity = %v, error = %v", identities, err)
	}
	access, _, err := f.machine.IssueWebSession(t.Context(), userID, identities[0].ID, netip.Addr{}, "stream")
	if err != nil {
		t.Fatal(err)
	}
	server := streamCredentialServer(f.machine, f.queries)
	eventv1connect.RegisterEventServiceHandler(server, f.events)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(auth.StreamTransport(mux))
	t.Cleanup(endpoint.Close)
	transport := connecthttp.NewTransport(endpoint.Client(), endpoint.URL)
	client := eventv1connect.NewEventServiceClient(connect.NewClient(transport))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Cookie", "__Host-loco_session="+access)
	info.RequestHeader().Set("Connect-Protocol-Version", "1")
	request := &eventv1.StreamEventsRequest{OrgId: f.orgID, WorkspaceId: workspaceID.String()}
	stream, err := client.StreamEvents(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Receive(); err != nil {
		t.Fatalf("initial event batch: %v", err)
	}
	if _, err := f.queries.SetUserStatus(t.Context(), genDb.SetUserStatusParams{
		ID: userID, Status: genDb.UserStatusSuspended,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Receive(); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("dashboard stream after suspension = %v, want Unauthenticated", err)
	}
}

func TestIdleEventStreamReportsDatabaseConnectionFailure(t *testing.T) {
	t.Parallel()
	f, userID, workspaceID := newStreamCredentialFixture(t)
	var closing atomic.Bool
	connecting := make(chan struct{})
	config := f.pool.Config()
	config.MinConns = 0
	config.MinIdleConns = 0
	config.BeforeConnect = func(ctx context.Context, _ *pgx.ConnConfig) error {
		if !closing.Load() {
			return nil
		}
		close(connecting)
		<-ctx.Done()
		return ctx.Err()
	}
	validationPool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validationPool.Close)
	queries := genDb.New(validationPool)
	machine := tvm.NewVendingMachine(queries, f.machine.Cfg)
	server := streamCredentialServer(machine, queries)
	eventv1connect.RegisterEventServiceHandler(server, f.events)
	token := issueStreamCredential(t, f, userID, workspaceID, streamSession, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := streamHTTPClients(t, server, token).Event.StreamEvents(ctx, &eventv1.StreamEventsRequest{
		OrgId: f.orgID, WorkspaceId: workspaceID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err = stream.Receive(); err != nil {
		t.Fatal(err)
	}
	closing.Store(true)
	validationPool.Reset()
	select {
	case <-connecting:
	case <-ctx.Done():
		t.Fatal("stream did not attempt a new database connection")
	}
	// Closing a pool cancels its connection constructor, not the RPC request.
	validationPool.Close()
	if _, err = stream.Receive(); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("database connection failure = %v, want Internal", err)
	}
}

func TestOwnedEventStreamSurvivesIssuerDeletionUntilRevocation(t *testing.T) {
	t.Parallel()
	f, userID, workspaceID := newStreamCredentialFixture(t)
	f.events.poll = time.Millisecond
	revoker, _, err := f.machine.Issue(
		t.Context(),
		"revoker",
		userID,
		genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: workspaceID},
		[]genDb.EntityScope{
			{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeAdmin},
		},
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	token := issueStreamCredential(t, f, userID, workspaceID, streamOwned, []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := streamCredentialClients(t, f, token).Event.StreamEvents(ctx, &eventv1.StreamEventsRequest{
		OrgId: f.orgID, WorkspaceId: workspaceID.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err = stream.Receive(); err != nil {
		t.Fatal(err)
	}
	if err = f.queries.DeleteUser(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	if err = events.Record(f.owner, f.queries, events.Event{
		Type: events.WorkspaceCreated, OrgID: new(uuid.MustParse(f.orgID)), WorkspaceID: &workspaceID,
	}); err != nil {
		t.Fatal(err)
	}
	publishEvents(t, f.pool)
	if _, err = stream.Receive(); err != nil {
		t.Fatalf("owned token after issuer deletion: %v", err)
	}
	// A second API instance revokes the first instance's credential through the RPC.
	// Its independently authenticated workspace token also survives issuer deletion.
	secondMachine := tvm.NewVendingMachine(f.queries, f.machine.Cfg)
	secondAPI := streamCredentialServer(secondMachine, f.queries)
	tokenv1connect.RegisterTokenServiceHandler(
		secondAPI,
		NewTokenServer(f.pool, f.queries, secondMachine, limits.Defaults()),
	)
	if _, err = streamHTTPClients(t, secondAPI, revoker).Token.RevokeToken(t.Context(), &tokenv1.RevokeTokenRequest{
		Name: "stream", EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, EntityId: workspaceID.String(),
	}); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = stream.Receive(); err != nil {
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("owned token after second API revocation = %v", err)
			}
			break
		}
	}
}

func newStreamCredentialFixture(t *testing.T) (*eventFixture, uuid.UUID, uuid.UUID) {
	t.Helper()
	f := newEventFixture(t)
	f.events.poll = time.Hour
	f.machine.Cfg.MaxAPITokenDuration = time.Hour
	principal, _ := reqctx.PrincipalFrom(f.owner)
	workspaceID, err := f.queries.CreateWorkspace(t.Context(), genDb.CreateWorkspaceParams{
		OrgID: uuid.MustParse(f.orgID), Name: "stream", CreatedBy: principal.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = events.Record(f.owner, f.queries, events.Event{
		Type: events.WorkspaceCreated, OrgID: new(uuid.MustParse(f.orgID)), WorkspaceID: &workspaceID,
	}); err != nil {
		t.Fatal(err)
	}
	publishEvents(t, f.pool)
	return f, principal.UserID, workspaceID
}

func issueStreamCredential(t *testing.T, f *eventFixture, userID, workspaceID uuid.UUID,
	kind streamCredential, scopes []genDb.EntityScope,
) string {
	t.Helper()
	if kind == streamSession {
		identities, err := f.queries.ListIdentitiesForUser(t.Context(), userID)
		if err != nil || len(identities) != 1 {
			t.Fatalf("fixture identity = %v, error = %v", identities, err)
		}
		token, _, err := f.machine.IssueSession(t.Context(), userID, &identities[0].ID, netip.Addr{}, "stream")
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	owner := genDb.Entity{Type: genDb.EntityTypeUser, ID: userID}
	if kind == streamOwned {
		owner = genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: workspaceID}
	}
	token, _, err := f.machine.Issue(t.Context(), "stream", userID, owner, scopes, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func streamCredentialClients(t *testing.T, f *eventFixture, token string) *rpc.Clients {
	t.Helper()
	server := streamCredentialServer(f.machine, f.queries)
	eventv1connect.RegisterEventServiceHandler(server, f.events)
	return streamHTTPClients(t, server, token)
}

func streamCredentialServer(machine *tvm.VendingMachine, queries *genDb.Queries) *connect.Server {
	return connect.NewServer(interceptor.NewErrorInterceptor(), interceptor.NewAuthInterceptor(
		machine, auth.NewVerifier(http.DefaultClient, nil), nil, authz.New(queries),
		auth.StreamConfig{Interval: 5 * time.Millisecond, Timeout: time.Second},
	).WebCookies(auth.NewWebCookies("https://app.loco.test", nil)).Interceptor())
}

func streamHTTPClients(t *testing.T, server *connect.Server, token string) *rpc.Clients {
	t.Helper()
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(auth.StreamTransport(mux))
	t.Cleanup(endpoint.Close)
	return rpc.ForHost(endpoint.URL, token)
}

func TestIdleDeploymentWatchClosesAfterRevocation(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	created, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID: f.userID, EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead,
	}); err != nil {
		t.Fatal(err)
	}
	machine := tvm.NewVendingMachine(f.queries, tvm.Config{MaxAPITokenDuration: time.Hour})
	token, _, err := machine.Issue(
		t.Context(),
		"stream",
		f.userID,
		genDb.Entity{Type: genDb.EntityTypeResource, ID: f.resourceID},
		[]genDb.EntityScope{
			{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
		},
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	deployments, _ := f.deploymentWriter(t)
	deployments.notifier = pgnotify.New(f.pool.Config().ConnConfig, "deployment_activity", time.Hour)
	server := streamCredentialServer(machine, f.queries)
	deploymentv1connect.RegisterDeploymentServiceHandler(server, deployments)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := streamHTTPClients(t, server, token).Deployment.WatchDeployment(ctx,
		&deploymentv1.WatchDeploymentRequest{DeploymentId: created.GetDeploymentId()})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err = stream.Receive(); err != nil {
		t.Fatal(err)
	}
	if err = machine.Revoke(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Receive(); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("idle deployment watch = %v, want Unauthenticated", err)
	}
}

func TestIdleLogTailClosesAfterRevocation(t *testing.T) {
	t.Parallel()
	clickhouse := newTestClickHouse(t)
	f, userID, workspaceID := newStreamCredentialFixture(t)
	token := issueStreamCredential(t, f, userID, workspaceID, streamOwned, []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	})
	server := streamCredentialServer(f.machine, f.queries)
	observabilityv1connect.RegisterObservabilityServiceHandler(server,
		NewObservabilityServer(clickhouse, authz.New(f.queries), f.queries, observabilityTestConfig))
	started := time.Now()
	insertLogs(t, clickhouse, started, "before revocation", map[string]string{
		workspaceAttribute: workspaceID.String(), environmentAttribute: uuid.NewString(),
	}, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := streamHTTPClients(t, server, token).Observability.TailLogs(
		ctx,
		&observabilityv1.TailLogsRequest{
			WorkspaceId: workspaceID.String(),
			Since:       timestamppb.New(started.Add(-time.Second)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	// Wait for one real log entry before revoking; the remaining stream is idle.
	if _, err = stream.Receive(); err != nil {
		t.Fatal(err)
	}
	if err = f.machine.Revoke(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Receive(); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("idle log tail = %v, want Unauthenticated", err)
	}
}
