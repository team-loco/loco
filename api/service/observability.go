package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/guardrails"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	chClient "github.com/team-loco/loco/internal/clickhouse"
	"github.com/team-loco/loco/internal/logquery"
	"github.com/team-loco/loco/internal/rpcerr"
	"github.com/team-loco/loco/internal/systemtenant"
)

// maxResourceReadChecks bounds the permission checks one request naming resources can cause.
const maxResourceReadChecks = 100

// systemScopeWorkspaceOnly names what a system-scope request cannot filter by.
const systemScopeWorkspaceOnly = "resource_ids and environment_id apply to workspaces, not to the installation scope"

var errObservabilityUnavailable = errors.New("observability is unavailable")

var _ observabilityv1connect.ObservabilityServiceHandler = (*ObservabilityServer)(nil)

// ObservabilityConfig bounds the logs, metrics and traces one request can read.
type ObservabilityConfig struct {
	Limits          guardrails.Limits
	MaxTailDuration time.Duration
}

// ObservabilityServer reads the telemetry the observability proxy writes to ClickHouse.
type ObservabilityServer struct {
	// clickhouse is nil when the installation has no ClickHouse; every call is then Unavailable.
	clickhouse *chClient.Client
	authz      *authz.Authorizer
	queries    *genDb.Queries
	cfg        ObservabilityConfig
}

func NewObservabilityServer(
	clickhouse *chClient.Client,
	authorizer *authz.Authorizer,
	queries *genDb.Queries,
	cfg ObservabilityConfig,
) *ObservabilityServer {
	return &ObservabilityServer{clickhouse: clickhouse, authz: authorizer, queries: queries, cfg: cfg}
}

func (s *ObservabilityServer) QueryLogs(
	ctx context.Context,
	req *observabilityv1.QueryLogsRequest,
) (*observabilityv1.QueryLogsResponse, error) {
	if err := guardrails.ValidateLogsRequest(req, s.cfg.Limits); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	query, err := s.logQuery(ctx, req)
	if err != nil {
		return nil, err
	}
	query.Start, query.End = req.GetStartTime().AsTime(), req.GetEndTime().AsTime()
	query.Limit = guardrails.ClampLimit(req.GetPageSize(), s.cfg.Limits)
	query.Cursor, query.Order, query.CountMatches = req.GetPageToken(), req.GetOrder(), req.GetIncludeTotalCount()
	response, err := s.clickhouse.QueryLogs(ctx, query)
	if errors.Is(err, chClient.ErrInvalidLogCursor) {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	return response, nil
}

func (s *ObservabilityServer) QueryLogBuckets(
	ctx context.Context,
	req *observabilityv1.QueryLogBucketsRequest,
) (*observabilityv1.QueryLogBucketsResponse, error) {
	if err := guardrails.ValidateLogBucketsRequest(req, s.cfg.Limits); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	query, err := s.logQuery(ctx, req)
	if err != nil {
		return nil, err
	}
	query.Start, query.End = req.GetStartTime().AsTime(), req.GetEndTime().AsTime()
	response, err := s.clickhouse.QueryLogBuckets(ctx, query, req.GetIntervalSeconds(), req.GetGroupBy())
	if err != nil {
		return nil, fmt.Errorf("query log buckets: %w", err)
	}
	return response, nil
}

// logFilterRequest holds the scope and query QueryLogsRequest and TailLogsRequest share.
type logFilterRequest interface {
	GetWorkspaceId() string
	GetEnvironmentId() string
	GetResourceIds() []string
	GetQuery() string
}

// logQuery parses the request's query, authorizes the caller and returns the scope and filter to read.
func (s *ObservabilityServer) logQuery(ctx context.Context, req logFilterRequest) (chClient.LogQuery, error) {
	filter, resources, err := s.parseQuery(
		ctx, req.GetQuery(), chClient.ValidateLogFilter,
		req.GetWorkspaceId(), req.GetEnvironmentId(), req.GetResourceIds(),
	)
	if err != nil {
		return chClient.LogQuery{}, err
	}
	return chClient.LogQuery{
		WorkspaceID:   req.GetWorkspaceId(),
		EnvironmentID: req.GetEnvironmentId(),
		ResourceIDs:   req.GetResourceIds(),
		Filter:        filter,
		Resources:     resources,
	}, nil
}

// parseQuery parses text and checks it with validate, authorizes the caller to read the workspace
// or resources and returns the query with the workspace's resource names when it has a @resource
// field. The system scope has no resources: it takes @component and @cluster instead.
func (s *ObservabilityServer) parseQuery(
	ctx context.Context,
	text string,
	validate func(logquery.Expr) error,
	workspaceID, environmentID string,
	resourceIDs []string,
) (logquery.Expr, []chClient.ResourceName, error) {
	query, err := logquery.Parse(text)
	if err == nil {
		err = validate(query)
	}
	if err != nil {
		return nil, nil, querySyntaxError(err)
	}
	if err = s.authorizeRead(ctx, workspaceID, environmentID, resourceIDs); err != nil {
		return nil, nil, err
	}
	if workspaceID == systemtenant.WorkspaceID {
		if query, err = systemQuery(query); err != nil {
			return nil, nil, querySyntaxError(err)
		}
		return query, nil, nil
	}
	if !logquery.Uses(query, chClient.ResourceField) {
		return query, nil, nil
	}
	resources, err := s.resourceNames(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	return query, resources, nil
}

func (s *ObservabilityServer) resourceNames(ctx context.Context, workspaceID string) ([]chClient.ResourceName, error) {
	workspace, err := parseID("workspace_id", workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListWorkspaceResourceSlugs(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list resource names: %w", err)
	}
	names := make([]chClient.ResourceName, len(rows))
	for i, row := range rows {
		names[i] = chClient.ResourceName{ID: row.ID.String(), Name: row.Slug}
	}
	return names, nil
}

func (s *ObservabilityServer) QueryMetrics(
	ctx context.Context,
	req *observabilityv1.QueryMetricsRequest,
) (*observabilityv1.QueryMetricsResponse, error) {
	if err := guardrails.ValidateMetricsRequest(req, s.cfg.Limits); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	if err := s.authorizeRead(
		ctx, req.GetWorkspaceId(), req.GetEnvironmentId(), req.GetResourceIds(),
	); err != nil {
		return nil, err
	}
	series, err := s.clickhouse.QueryMetrics(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	return &observabilityv1.QueryMetricsResponse{Series: series}, nil
}

func (s *ObservabilityServer) ListTraces(
	ctx context.Context,
	req *observabilityv1.ListTracesRequest,
) (*observabilityv1.ListTracesResponse, error) {
	if err := guardrails.ValidateListTracesRequest(req, s.cfg.Limits); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	filter, err := s.traceFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	if req.GetOrderBy() == observabilityv1.TraceOrder_TRACE_ORDER_RESOURCE && len(filter.Resources) == 0 {
		filter.Resources, err = s.resourceNames(ctx, req.GetScope().GetWorkspaceId())
		if err != nil {
			return nil, err
		}
	}
	response, err := s.clickhouse.ListTraces(ctx, req, filter, normalizePageSize(req.GetPageSize()))
	if errors.Is(err, chClient.ErrInvalidTracePageToken) {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
	}
	if err != nil {
		return nil, fmt.Errorf("list traces: %w", err)
	}
	return response, nil
}

func (s *ObservabilityServer) QueryTraceBuckets(
	ctx context.Context,
	req *observabilityv1.QueryTraceBucketsRequest,
) (*observabilityv1.QueryTraceBucketsResponse, error) {
	if err := guardrails.ValidateTraceBucketsRequest(req, s.cfg.Limits); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	filter, err := s.traceFilter(ctx, req)
	if err != nil {
		return nil, err
	}
	response, err := s.clickhouse.QueryTraceBuckets(ctx, req, filter)
	if err != nil {
		return nil, fmt.Errorf("query trace buckets: %w", err)
	}
	return response, nil
}

// traceFilterRequest holds the scope and query ListTracesRequest and QueryTraceBucketsRequest share.
type traceFilterRequest interface {
	GetScope() *observabilityv1.TraceScope
	GetQuery() string
}

// traceFilter parses the request's query, authorizes the caller and returns the filter to apply.
func (s *ObservabilityServer) traceFilter(ctx context.Context, req traceFilterRequest) (chClient.TraceFilter, error) {
	scope := req.GetScope()
	query, resources, err := s.parseQuery(
		ctx, req.GetQuery(), chClient.ValidateTraceFilter,
		scope.GetWorkspaceId(), scope.GetEnvironmentId(), scope.GetResourceIds(),
	)
	if err != nil {
		return chClient.TraceFilter{}, err
	}
	return chClient.TraceFilter{Query: query, Resources: resources}, nil
}

func (s *ObservabilityServer) GetTrace(
	ctx context.Context,
	req *observabilityv1.GetTraceRequest,
) (*observabilityv1.GetTraceResponse, error) {
	if err := guardrails.ValidateTraceScope(req.GetScope()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	scope := req.GetScope()
	if err := s.authorizeRead(
		ctx, scope.GetWorkspaceId(), scope.GetEnvironmentId(), scope.GetResourceIds(),
	); err != nil {
		return nil, err
	}
	response, err := s.clickhouse.GetTrace(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("get trace: %w", err)
	}
	return response, nil
}

// authorizeRead checks that the caller can read the workspace or, for a request that names
// resources, every named resource. Every ClickHouse query also filters on the workspace, so a
// named resource from another workspace matches no rows. The system tenant's ID asks for the
// installation scope, which needs system read and takes no resources or environment.
func (s *ObservabilityServer) authorizeRead(
	ctx context.Context,
	workspaceID, environmentID string,
	resourceIDs []string,
) error {
	if s.clickhouse == nil {
		return rpcerr.New(connect.CodeUnavailable, errObservabilityUnavailable)
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return err
	}
	if workspaceID == systemtenant.WorkspaceID {
		return s.authorizeSystemRead(ctx, p.Scopes, environmentID, resourceIDs)
	}
	workspace, err := parseID("workspace_id", workspaceID)
	if err != nil {
		return err
	}
	resources := make([]uuid.UUID, len(resourceIDs))
	for i, raw := range resourceIDs {
		if resources[i], err = parseID("resource_ids", raw); err != nil {
			return err
		}
	}
	err = s.canRead(ctx, p.Scopes, workspace, resources)
	if accessDenied(err) {
		return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	if err != nil {
		return fmt.Errorf("check access: %w", err)
	}
	return nil
}

func (s *ObservabilityServer) authorizeSystemRead(
	ctx context.Context,
	scopes []genDb.EntityScope,
	environmentID string,
	resourceIDs []string,
) error {
	err := s.authz.Check(ctx, scopes, actions.NewSystem(actions.ReadSystemTelemetry))
	if accessDenied(err) {
		return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	if err != nil {
		return fmt.Errorf("check access: %w", err)
	}
	if environmentID != "" || len(resourceIDs) > 0 {
		return connect.NewError(connect.CodeInvalidArgument, systemScopeWorkspaceOnly)
	}
	return nil
}

// systemQuery rejects the workspace fields a system query cannot use and reads @component and
// @cluster as the service and cluster attributes of Loco's own telemetry.
func systemQuery(expr logquery.Expr) (logquery.Expr, error) {
	err := logquery.CheckFields(expr, func(field logquery.Field) error {
		if field.Key == chClient.ResourceField || field.Key == chClient.BuildField {
			return &logquery.SyntaxError{
				Position: field.Position,
				Message:  "@" + field.Key + " applies to workspaces, not to the installation scope",
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return chClient.SystemFields(expr), nil
}

// canRead checks the resources one by one, so the cap bounds the lookups one request can cause.
func (s *ObservabilityServer) canRead(
	ctx context.Context,
	scopes []genDb.EntityScope,
	workspace uuid.UUID,
	resources []uuid.UUID,
) error {
	err := s.authz.Check(ctx, scopes, actions.New(actions.ReadWorkspaceTelemetry, workspace))
	if !accessDenied(err) || len(resources) == 0 {
		return err
	}
	if len(resources) > maxResourceReadChecks {
		return fmt.Errorf("%w: %d resources named, at most %d",
			authz.ErrInsufficientPermissions, len(resources), maxResourceReadChecks)
	}
	for _, id := range resources {
		if err := s.authz.Check(ctx, scopes, actions.New(actions.ReadResourceTelemetry, id)); err != nil {
			return err
		}
	}
	return nil
}
