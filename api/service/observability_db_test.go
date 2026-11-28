package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	chClient "github.com/team-loco/loco/internal/clickhouse"
	"github.com/team-loco/loco/internal/systemtenant"
)

const (
	workspaceReader = "workspace-reader"
	orgReader       = "org-reader"
	resourceReader  = "resource-reader"
	workspaceWriter = "workspace-writer"
	stranger        = "stranger"
	systemReader    = "system-reader"
	systemAdmin     = "system-admin"
)

// observabilityFixture is an organization with two workspaces; the first holds two resources.
type observabilityFixture struct {
	org             uuid.UUID
	workspace       uuid.UUID
	otherWorkspace  uuid.UUID
	resource        uuid.UUID
	otherResource   uuid.UUID
	foreignResource uuid.UUID
	callers         map[string][]genDb.EntityScope
	authorizer      *authz.Authorizer
	queries         *genDb.Queries
}

func newObservabilityFixture(t *testing.T) *observabilityFixture {
	t.Helper()
	pool := dbtest.NewPool(t)
	queries := genDb.New(pool)
	f := &observabilityFixture{authorizer: authz.New(queries), queries: queries}
	tn := seedTenant(t, pool, queries, "observability@example.com", "org")
	f.org = tn.orgID
	f.workspace = tn.workspace(t, "ws")
	f.otherWorkspace = tn.workspace(t, "other")
	f.resource = tn.resource(t, f.workspace, "web", &resourcev1.ServiceSpec{})
	f.otherResource = tn.resource(t, f.workspace, "worker", &resourcev1.ServiceSpec{})
	f.foreignResource = tn.resource(t, f.otherWorkspace, "foreign", &resourcev1.ServiceSpec{})
	f.callers = map[string][]genDb.EntityScope{
		workspaceReader: {{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspace, Scope: genDb.ScopeRead}},
		orgReader:       {{EntityType: genDb.EntityTypeOrganization, EntityID: f.org, Scope: genDb.ScopeRead}},
		resourceReader:  {{EntityType: genDb.EntityTypeResource, EntityID: f.resource, Scope: genDb.ScopeRead}},
		workspaceWriter: {{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspace, Scope: genDb.ScopeWrite}},
		systemReader:    {{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeRead}},
		systemAdmin:     {{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin}},
	}
	return f
}

func (f *observabilityFixture) client(t *testing.T, clickhouse *chClient.Client) observabilityClient {
	t.Helper()
	return observabilityTestClient(
		t,
		NewObservabilityServer(clickhouse, f.authorizer, f.queries, observabilityTestConfig),
		f.callers,
	)
}

// A read call's workspace argument is a workspace ID or one of these scope selections.
const (
	installationScope = "installation"
	bothScopes        = "both"
	noScope           = "neither"
)

func scopeWorkspace(scope string) string {
	switch scope {
	case installationScope, noScope:
		return ""
	case bothScopes:
		return observabilityTestWorkspace
	}
	return scope
}

func scopeInstallation(scope string) bool { return scope == installationScope || scope == bothScopes }

func traceScope(workspace string, resources []string) *observabilityv1.TraceScope {
	return &observabilityv1.TraceScope{
		WorkspaceId: scopeWorkspace(workspace), Installation: scopeInstallation(workspace), ResourceIds: resources,
	}
}

type readCall func(ctx context.Context, client observabilityClient, workspace string, resources []string) error

func readCalls() map[string]readCall {
	window := func() (*timestamppb.Timestamp, *timestamppb.Timestamp) {
		now := time.Now()
		return timestamppb.New(now.Add(-time.Minute)), timestamppb.New(now)
	}
	return map[string]readCall{
		"QueryLogs": func(ctx context.Context, client observabilityClient, workspace string, resources []string) error {
			start, end := window()
			_, err := client.QueryLogs(ctx, &observabilityv1.QueryLogsRequest{
				WorkspaceId: scopeWorkspace(workspace), Installation: scopeInstallation(workspace),
				ResourceIds: resources, StartTime: start, EndTime: end,
			})
			return err
		},
		"TailLogs": func(ctx context.Context, client observabilityClient, workspace string, resources []string) error {
			stream, err := client.TailLogs(ctx, &observabilityv1.TailLogsRequest{
				WorkspaceId: scopeWorkspace(workspace), Installation: scopeInstallation(workspace),
				ResourceIds: resources,
			})
			if err != nil {
				return err
			}
			defer stream.Close()
			_, err = stream.Receive()
			return err
		},
		"QueryMetrics": func(ctx context.Context, client observabilityClient, workspace string, resources []string) error {
			start, end := window()
			_, err := client.QueryMetrics(ctx, &observabilityv1.QueryMetricsRequest{
				WorkspaceId: scopeWorkspace(workspace), Installation: scopeInstallation(workspace),
				ResourceIds: resources, StartTime: start, EndTime: end,
				MetricName: "http.server.requests", IntervalSeconds: 60, Aggregation: "max",
			})
			return err
		},
		"ListTraces": func(ctx context.Context, client observabilityClient, workspace string, resources []string) error {
			start, end := window()
			_, err := client.ListTraces(ctx, &observabilityv1.ListTracesRequest{
				Scope:     traceScope(workspace, resources),
				StartTime: start, EndTime: end,
			})
			return err
		},
		"QueryLogBuckets": func(ctx context.Context, client observabilityClient, workspace string, resources []string) error {
			start, end := window()
			_, err := client.QueryLogBuckets(ctx, &observabilityv1.QueryLogBucketsRequest{
				WorkspaceId: scopeWorkspace(workspace), Installation: scopeInstallation(workspace),
				ResourceIds: resources, StartTime: start, EndTime: end,
				IntervalSeconds: 60, GroupBy: observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_LEVEL,
			})
			return err
		},
		"QueryTraceBuckets": func(
			ctx context.Context,
			client observabilityClient,
			workspace string,
			resources []string,
		) error {
			start, end := window()
			_, err := client.QueryTraceBuckets(ctx, &observabilityv1.QueryTraceBucketsRequest{
				Scope:     traceScope(workspace, resources),
				StartTime: start, EndTime: end, IntervalSeconds: 60,
			})
			return err
		},
		"GetTrace": func(ctx context.Context, client observabilityClient, workspace string, resources []string) error {
			_, err := client.GetTrace(ctx, &observabilityv1.GetTraceRequest{
				Scope:   traceScope(workspace, resources),
				TraceId: observabilityTestTraceID,
			})
			return err
		},
	}
}

func TestObservabilityReadsNeedWorkspaceReadOrReadOnEveryNamedResource(t *testing.T) {
	t.Parallel()
	f := newObservabilityFixture(t)
	workspace, otherWorkspace := f.workspace.String(), f.otherWorkspace.String()
	resource, otherResource := f.resource.String(), f.otherResource.String()
	system := installationScope
	// A request that passes authorization reaches the unreachable ClickHouse and fails as Internal.
	cases := []struct {
		name      string
		caller    string
		workspace string
		resources []string
		code      connect.Code
	}{
		{"no token", "", workspace, nil, connect.CodeUnauthenticated},
		{"workspace reader", workspaceReader, workspace, nil, connect.CodeInternal},
		{
			"workspace reader naming a resource",
			workspaceReader,
			workspace,
			[]string{otherResource},
			connect.CodeInternal,
		},
		{"workspace reader in another workspace", workspaceReader, otherWorkspace, nil, connect.CodePermissionDenied},
		{"organization reader", orgReader, otherWorkspace, nil, connect.CodeInternal},
		{"workspace writer", workspaceWriter, workspace, nil, connect.CodePermissionDenied},
		{"caller without scopes", stranger, workspace, nil, connect.CodePermissionDenied},
		{"unknown workspace", workspaceReader, uuid.NewString(), nil, connect.CodePermissionDenied},
		{"resource reader naming its resource", resourceReader, workspace, []string{resource}, connect.CodeInternal},
		{"resource reader naming no resource", resourceReader, workspace, nil, connect.CodePermissionDenied},
		{
			"resource reader naming another resource too",
			resourceReader, workspace, []string{resource, otherResource}, connect.CodePermissionDenied,
		},
		{"system reader in the system scope", systemReader, system, nil, connect.CodeInternal},
		{"system admin in the system scope", systemAdmin, system, nil, connect.CodeInternal},
		{"workspace reader in the system scope", workspaceReader, system, nil, connect.CodePermissionDenied},
		{"organization reader in the system scope", orgReader, system, nil, connect.CodePermissionDenied},
		{"resource reader in the system scope", resourceReader, system, nil, connect.CodePermissionDenied},
		{"caller without scopes in the system scope", stranger, system, nil, connect.CodePermissionDenied},
		{"both workspace and installation", systemReader, bothScopes, nil, connect.CodeInvalidArgument},
		{"neither workspace nor installation", systemReader, noScope, nil, connect.CodeInvalidArgument},
		{
			"system admin naming the system tenant as a workspace",
			systemAdmin, systemtenant.WorkspaceID, nil, connect.CodePermissionDenied,
		},
		{
			"system reader naming the system tenant as a workspace",
			systemReader, systemtenant.WorkspaceID, nil, connect.CodePermissionDenied,
		},
		{"system reader naming a resource", systemReader, system, []string{resource}, connect.CodeInvalidArgument},
		{
			"workspace reader naming a resource in the system scope",
			workspaceReader, system, []string{resource}, connect.CodePermissionDenied,
		},
		{
			"resource reader at the cap",
			resourceReader, workspace, slices.Repeat([]string{resource}, maxResourceReadChecks), connect.CodeInternal,
		},
		{
			"resource reader over the cap",
			resourceReader, workspace, slices.Repeat([]string{resource}, maxResourceReadChecks+1),
			connect.CodePermissionDenied,
		},
	}
	client := f.client(t, unreachableClickHouse(t))
	for name, call := range readCalls() {
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				err := call(callerContext(t, tc.caller), client, tc.workspace, tc.resources)
				if connect.CodeOf(err) != tc.code {
					t.Fatalf("error = %v, want %v", err, tc.code)
				}
			})
		}
	}
}

func TestRequestMetricsAuthorizesTheNamedResource(t *testing.T) {
	f := newObservabilityFixture(t)
	client := f.client(t, unreachableClickHouse(t))
	for _, tc := range []struct {
		name, caller, workspace, resource string
		code                              connect.Code
	}{
		{"workspace read grant", workspaceReader, f.workspace.String(), f.resource.String(), connect.CodeInternal},
		{"direct resource grant", resourceReader, f.workspace.String(), f.resource.String(), connect.CodeInternal},
		{"another resource", resourceReader, f.workspace.String(), f.otherResource.String(), connect.CodePermissionDenied},
		{
			"outside the selected workspace", workspaceReader, f.otherWorkspace.String(),
			f.foreignResource.String(), connect.CodePermissionDenied,
		},
		{"stranger", stranger, f.workspace.String(), f.resource.String(), connect.CodePermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			end := time.Now()
			_, err := client.QueryRequestMetrics(
				callerContext(t, tc.caller),
				&observabilityv1.QueryRequestMetricsRequest{
					WorkspaceId:     tc.workspace,
					ResourceId:      tc.resource,
					EnvironmentId:   testEnvironmentIDForRequestMetrics,
					StartTime:       timestamppb.New(end.Add(-time.Minute)),
					EndTime:         timestamppb.New(end),
					IntervalSeconds: 60,
				},
			)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("error=%v, want %v", err, tc.code)
			}
		})
	}
}

const testEnvironmentIDForRequestMetrics = "66666666-7777-8888-9999-000000000000"
