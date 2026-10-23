package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"connectrpc.com/validate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/tvm"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/gen/go/loco/cluster/v1/clusterv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

func clusterAdminHTTP(t *testing.T, pool *pgxpool.Pool, machine *tvm.VendingMachine) string {
	t.Helper()
	queries := genDb.New(pool)
	server := connect.NewServer(
		interceptor.NewErrorInterceptor(),
		interceptor.NewContextInterceptor().Interceptor(),
		interceptor.NewAuthInterceptor(machine, nil, nil, authz.New(pool, queries)).Interceptor(),
		validate.NewServerInterceptor(),
	)
	clusterv1connect.RegisterClusterAdminServiceHandler(
		server,
		NewClusterAdminServer(pool, queries, testAgentConfig().JoinTokenTTL),
	)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return httpServer.URL
}

func TestClusterAdminHTTPRequiresSystemAdminForEveryMethod(t *testing.T) {
	f := newMembershipOrg(t)
	a, ctx := f.api, t.Context()
	a.machine.Cfg.MaxAPITokenDuration = time.Hour
	var clusterID uuid.UUID
	if err := a.pool.QueryRow(ctx, `INSERT INTO clusters (name, region, provider, is_active, is_default)
VALUES ('admin-test', 'east', 'kubernetes', true, false) RETURNING id`).
		Scan(&clusterID); err != nil {
		t.Fatal(err)
	}
	enrollTestCluster(t, a.pool, clusterID)
	endpoint := clusterAdminHTTP(t, a.pool, a.machine)
	grant := func(user membershipUser, entityType genDb.EntityType, entityID uuid.UUID, scope genDb.Scope) {
		t.Helper()
		if err := a.queries.AddUserScope(
			ctx,
			genDb.AddUserScopeParams{UserID: user.id, EntityType: entityType, EntityID: entityID, Scope: scope},
		); err != nil {
			t.Fatal(err)
		}
	}
	ordinary := a.signUp(t, "ordinary")
	workspaceAdmin := a.signUp(t, "workspace-admin")
	workspaceID := uuid.MustParse(f.shared)
	grant(workspaceAdmin, genDb.EntityTypeWorkspace, workspaceID, genDb.ScopeAdmin)
	grant(workspaceAdmin, genDb.EntityTypeWorkspace, workspaceID, genDb.ScopeRead)
	systemReader := a.signUp(t, "system-reader")
	grant(systemReader, genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeRead)
	systemWriter := a.signUp(t, "system-writer")
	grant(systemWriter, genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeWrite)
	apiToken, tokenErr := a.machine.Issue(
		ctx,
		"limited-token",
		workspaceAdmin.id.String(),
		genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: workspaceID},
		[]genDb.EntityScope{{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead}},
		time.Hour,
	)
	if tokenErr != nil {
		t.Fatal(tokenErr)
	}
	methods := map[string]func(clusterv1connect.ClusterAdminServiceClient) error{
		"create": func(client clusterv1connect.ClusterAdminServiceClient) error {
			_, err := client.CreateCluster(
				ctx,
				&clusterv1.CreateClusterRequest{Name: "created", Region: "east", Tier: environmentTypeProduction},
			)
			return err
		},
		"join-token": func(client clusterv1connect.ClusterAdminServiceClient) error {
			_, err := client.IssueJoinToken(ctx, &clusterv1.IssueJoinTokenRequest{ClusterId: clusterID.String()})
			return err
		},
		"list": func(client clusterv1connect.ClusterAdminServiceClient) error {
			_, err := client.ListClusters(ctx, &clusterv1.ListClustersRequest{})
			return err
		},
		"pause": func(client clusterv1connect.ClusterAdminServiceClient) error {
			_, err := client.PauseCluster(ctx, &clusterv1.PauseClusterRequest{ClusterId: clusterID.String()})
			return err
		},
		"resume": func(client clusterv1connect.ClusterAdminServiceClient) error {
			_, err := client.ResumeCluster(ctx, &clusterv1.ResumeClusterRequest{ClusterId: clusterID.String()})
			return err
		},
		"remove": func(client clusterv1connect.ClusterAdminServiceClient) error {
			_, err := client.RemoveCluster(
				ctx,
				&clusterv1.RemoveClusterRequest{ClusterId: clusterID.String(), Force: true},
			)
			return err
		},
		"restore": func(client clusterv1connect.ClusterAdminServiceClient) error {
			_, err := client.RestoreCluster(ctx, &clusterv1.RestoreClusterRequest{ClusterId: clusterID.String()})
			return err
		},
	}
	before, err := a.queries.ListAdminClusters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []struct {
		name, token string
		code        connect.Code
	}{
		{"anonymous", "", connect.CodeUnauthenticated},
		{"agent assertion", testAssertion(t, clusterID), connect.CodeUnauthenticated},
		{"ordinary user", ordinary.token, connect.CodePermissionDenied},
		{"organization admin", f.owner.token, connect.CodePermissionDenied},
		{"workspace admin", workspaceAdmin.token, connect.CodePermissionDenied},
		{"workspace API token", apiToken, connect.CodePermissionDenied},
		{"system read", systemReader.token, connect.CodePermissionDenied},
		{"system write", systemWriter.token, connect.CodePermissionDenied},
	} {
		t.Run(candidate.name, func(t *testing.T) {
			client := rpc.NewHTTPClients(http.DefaultClient, endpoint, candidate.token).ClusterAdmin
			for name, call := range methods {
				if callErr := call(client); connect.CodeOf(callErr) != candidate.code {
					t.Fatalf("%s: %v, want %v", name, callErr, candidate.code)
				}
			}
		})
	}
	after, err := a.queries.ListAdminClusters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("unauthorized calls changed cluster: %+v", after)
	}
	var generation int64
	if generationErr := a.pool.QueryRow(ctx, "SELECT sync_generation FROM clusters WHERE id = $1", clusterID).
		Scan(&generation); generationErr != nil {
		t.Fatal(generationErr)
	}
	if generation != 0 {
		t.Fatalf("unauthorized calls changed generation to %d", generation)
	}
	admin := a.signUp(t, "loco-admin")
	grant(admin, genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeAdmin)
	client := rpc.NewHTTPClients(http.DefaultClient, endpoint, admin.token).ClusterAdmin
	listed, err := client.ListClusters(ctx, &clusterv1.ListClustersRequest{})
	if err != nil || len(listed.GetClusters()) != 1 {
		t.Fatalf("admin list: %v, %v", listed, err)
	}
	for _, name := range []string{"pause", "remove", "restore", "resume"} {
		if err := methods[name](client); err != nil {
			t.Fatalf("admin %s: %v", name, err)
		}
	}
	if err := a.queries.RemoveUserScope(
		ctx,
		genDb.RemoveUserScopeParams{
			UserID:     admin.id,
			EntityType: genDb.EntityTypeSystem,
			EntityID:   uuid.Nil,
			Scope:      genDb.ScopeAdmin,
		},
	); err != nil {
		t.Fatal(err)
	}
	for name, call := range methods {
		if err := call(client); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("revoked admin %s: %v", name, err)
		}
	}
}

func TestClusterAdminHTTPRemovalReportsWorkloadsAndRestoresIdentity(t *testing.T) {
	f := newBuildFixture(t)
	ctx := t.Context()
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatal(err)
	}
	var userID uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT id FROM users LIMIT 1").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := f.queries.AddUserScope(
		ctx,
		genDb.AddUserScopeParams{
			UserID:     userID,
			EntityType: genDb.EntityTypeSystem,
			EntityID:   uuid.Nil,
			Scope:      genDb.ScopeAdmin,
		},
	); err != nil {
		t.Fatal(err)
	}
	machine := tvm.NewVendingMachine(
		f.pool,
		f.queries,
		tvm.Config{
			SessionAccessTokenDuration:  time.Hour,
			SessionRefreshTokenDuration: time.Hour,
			LastUsedUpdateInterval:      time.Hour,
		},
	)
	t.Cleanup(machine.Close)
	token, _, issueErr := machine.IssueSession(ctx, userID, nil, "", "admin-test")
	if issueErr != nil {
		t.Fatal(issueErr)
	}
	endpoint := clusterAdminHTTP(t, f.pool, machine)
	client := rpc.NewHTTPClients(http.DefaultClient, endpoint, token).ClusterAdmin
	id := f.clusterID.String()
	if _, err := client.RemoveCluster(
		ctx,
		&clusterv1.RemoveClusterRequest{ClusterId: id},
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("remove unpaused: %v", err)
	}
	if _, err := client.PauseCluster(ctx, &clusterv1.PauseClusterRequest{ClusterId: id}); err != nil {
		t.Fatal(err)
	}
	_, removeErr := client.RemoveCluster(ctx, &clusterv1.RemoveClusterRequest{ClusterId: id})
	connectErr, ok := errors.AsType[*connect.Error](removeErr)
	if !ok || connectErr.Code() != connect.CodeFailedPrecondition || len(connectErr.Details()) != 1 {
		t.Fatalf("normal removal: %v", removeErr)
	}
	detail, detailErr := connectproto.UnmarshalErrorDetail(connectErr.Details()[0])
	if detailErr != nil {
		t.Fatal(detailErr)
	}
	workloads, ok := detail.(*clusterv1.ClusterWorkloads)
	if !ok || len(workloads.GetWorkloads()) == 0 {
		t.Fatalf("missing workload IDs: %v", detail)
	}
	removed, forceErr := client.RemoveCluster(ctx, &clusterv1.RemoveClusterRequest{ClusterId: id, Force: true})
	if forceErr != nil || len(removed.GetWorkloads()) != len(workloads.GetWorkloads()) {
		t.Fatalf("forced removal: %v, %v", removed, forceErr)
	}
	if _, err := client.ResumeCluster(
		ctx,
		&clusterv1.ResumeClusterRequest{ClusterId: id},
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("resume removed: %v", err)
	}
	if _, err := client.RestoreCluster(ctx, &clusterv1.RestoreClusterRequest{ClusterId: id}); err != nil {
		t.Fatal(err)
	}
	row, err := f.queries.GetClusterByID(ctx, f.clusterID)
	if err != nil || row.IsActive || row.LastHeartbeat != nil || row.HealthStatus != nil {
		t.Fatalf("restored health/state: %+v, %v", row, err)
	}
	if _, err := client.ResumeCluster(ctx, &clusterv1.ResumeClusterRequest{ClusterId: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PauseCluster(
		ctx,
		&clusterv1.PauseClusterRequest{ClusterId: "invalid"},
	); connect.CodeOf(
		err,
	) != connect.CodeInvalidArgument {
		t.Fatalf("invalid ID: %v", err)
	}
	if _, err := client.PauseCluster(
		ctx,
		&clusterv1.PauseClusterRequest{ClusterId: uuid.NewString()},
	); connect.CodeOf(
		err,
	) != connect.CodeNotFound {
		t.Fatalf("unknown ID: %v", err)
	}
}
