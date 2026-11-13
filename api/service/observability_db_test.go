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
	chClient "github.com/team-loco/loco/internal/clickhouse"
)

const (
	workspaceReader = "workspace-reader"
	orgReader       = "org-reader"
	resourceReader  = "resource-reader"
	workspaceWriter = "workspace-writer"
	stranger        = "stranger"
)

// observabilityFixture is an organization with two workspaces; the first holds two resources.
type observabilityFixture struct {
	org            uuid.UUID
	workspace      uuid.UUID
	otherWorkspace uuid.UUID
	resource       uuid.UUID
	otherResource  uuid.UUID
	callers        map[string][]genDb.EntityScope
	authorizer     *authz.Authorizer
}

func newObservabilityFixture(t *testing.T) *observabilityFixture {
	t.Helper()
	pool := dbtest.NewPool(t)
	f := &observabilityFixture{authorizer: authz.New(pool, genDb.New(pool))}
	row := pool.QueryRow(context.Background(), `
WITH u AS (
    INSERT INTO users (email) VALUES ('observability@example.com') RETURNING id
), o AS (
    INSERT INTO organizations (name, created_by) SELECT 'org', id FROM u RETURNING id
), w AS (
    INSERT INTO workspaces (org_id, name, created_by) SELECT o.id, 'ws', u.id FROM o, u RETURNING id
), w2 AS (
    INSERT INTO workspaces (org_id, name, created_by) SELECT o.id, 'other', u.id FROM o, u RETURNING id
), r AS (
    INSERT INTO resources (workspace_id, name, slug, type, description, spec, spec_version)
    SELECT w.id, 'web', 'web', 'service', '', '{}', 1 FROM w RETURNING id
), r2 AS (
    INSERT INTO resources (workspace_id, name, slug, type, description, spec, spec_version)
    SELECT w.id, 'worker', 'worker', 'service', '', '{}', 1 FROM w RETURNING id
)
SELECT o.id, w.id, w2.id, r.id, r2.id FROM o, w, w2, r, r2`)
	if err := row.Scan(&f.org, &f.workspace, &f.otherWorkspace, &f.resource, &f.otherResource); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.callers = map[string][]genDb.EntityScope{
		workspaceReader: {{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspace, Scope: genDb.ScopeRead}},
		orgReader:       {{EntityType: genDb.EntityTypeOrganization, EntityID: f.org, Scope: genDb.ScopeRead}},
		resourceReader:  {{EntityType: genDb.EntityTypeResource, EntityID: f.resource, Scope: genDb.ScopeRead}},
		workspaceWriter: {{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspace, Scope: genDb.ScopeWrite}},
	}
	return f
}

func (f *observabilityFixture) client(t *testing.T, clickhouse *chClient.Client) observabilityClient {
	t.Helper()
	return observabilityTestClient(
		t,
		NewObservabilityServer(clickhouse, f.authorizer, observabilityTestConfig),
		f.callers,
	)
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
				WorkspaceId: workspace, ResourceIds: resources, StartTime: start, EndTime: end,
			})
			return err
		},
		"TailLogs": func(ctx context.Context, client observabilityClient, workspace string, resources []string) error {
			stream, err := client.TailLogs(ctx, &observabilityv1.TailLogsRequest{
				WorkspaceId: workspace, ResourceIds: resources,
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
				WorkspaceId: workspace, ResourceIds: resources, StartTime: start, EndTime: end,
				MetricName: "http.server.requests", IntervalSeconds: 60, Aggregation: "max",
			})
			return err
		},
		"ListTraces": func(ctx context.Context, client observabilityClient, workspace string, resources []string) error {
			start, end := window()
			_, err := client.ListTraces(ctx, &observabilityv1.ListTracesRequest{
				Scope:     &observabilityv1.TraceScope{WorkspaceId: workspace, ResourceIds: resources},
				StartTime: start, EndTime: end,
			})
			return err
		},
		"GetTrace": func(ctx context.Context, client observabilityClient, workspace string, resources []string) error {
			_, err := client.GetTrace(ctx, &observabilityv1.GetTraceRequest{
				Scope:   &observabilityv1.TraceScope{WorkspaceId: workspace, ResourceIds: resources},
				TraceId: observabilityTestTraceID,
			})
			return err
		},
	}
}

func TestObservabilityReadsNeedWorkspaceReadOrReadOnEveryNamedResource(t *testing.T) {
	f := newObservabilityFixture(t)
	workspace, otherWorkspace := f.workspace.String(), f.otherWorkspace.String()
	resource, otherResource := f.resource.String(), f.otherResource.String()
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
