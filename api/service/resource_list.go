package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

// resourceListSQL defines the resources ListWorkspaceResources matches as the CTE matched, with
// the values its filters and facets read, scoped to $3's environment when it is set, and the
// per-environment sort values, scoped to $10's.
// $1 workspace, $2 cluster silence in seconds, $3 environment or NULL, $4 ILIKE pattern or ”,
// $5 status name or ”, $6 not deployed, $7 region or ”, $8 type or ”, $9 environment scoped,
// $10 sort environment or NULL, $11 the readable resources, or NULL for every resource.
const resourceListSQL = `WITH ws AS MATERIALIZED (
  SELECT r.id, r.name, r.slug, r.description, r.type::text AS type, r.created_at, r.spec
  FROM resources r WHERE r.workspace_id = $1 AND ($11::uuid[] IS NULL OR r.id = ANY($11::uuid[]))
),
regions AS MATERIALIZED (
  SELECT i.resource_id, i.environment_id, i.region, i.desired_replicas,
    d.id AS deployment_id, d.created_at AS deployment_created_at, d.spec AS deployment_spec,
    d.spec->'build'->>'image' AS image,
    region_status(region_status_cause(
        i.last_heartbeat, i.health_status, make_interval(secs => $2::float8),
        i.ready_replicas, i.desired_replicas, i.placement_status, cardinality(i.unserved_domains)
      ), i.ready_replicas, i.desired_replicas, i.placement_status) AS status
  FROM resource_region_inputs i
  JOIN deployments d ON d.id = i.deployment_id
  WHERE i.resource_id = ANY(ARRAY(SELECT id FROM ws))
    AND ($3::uuid IS NULL OR $10::uuid IS NULL OR i.environment_id IN ($3::uuid, $10::uuid))
),
cells AS (
  SELECT resource_id, environment_id, environment_status(array_agg(status)) AS status,
    min(region) AS first_region,
    (array_agg(deployment_spec ORDER BY deployment_created_at DESC, deployment_id DESC))[1] AS live_spec
  FROM regions GROUP BY resource_id, environment_id
),
facts AS (
  SELECT ws.id, ws.name, ws.slug, ws.description, ws.type, ws.created_at, lower(ws.name) AS name_key,
    ARRAY(SELECT c.status FROM cells c WHERE c.resource_id = ws.id
      AND ($3::uuid IS NULL OR c.environment_id = $3::uuid)) AS statuses,
    ARRAY(SELECT c.status FROM cells c WHERE c.resource_id = ws.id
      AND ($10::uuid IS NULL OR c.environment_id = $10::uuid)) AS sort_statuses,
    ARRAY(SELECT g.region FROM regions g
      WHERE g.resource_id = ws.id AND ($3::uuid IS NULL OR g.environment_id = $3::uuid)
      UNION SELECT t.key FROM jsonb_each(ws.spec->'regions') t WHERE t.value->>'enabled' = 'true'
      ORDER BY 1) AS regions,
    ARRAY(SELECT DISTINCT g.image FROM regions g
      WHERE g.resource_id = ws.id AND g.image <> '' AND ($3::uuid IS NULL OR g.environment_id = $3::uuid)) AS images,
    (SELECT COALESCE(sum(g.desired_replicas), 0) FROM regions g
      WHERE g.resource_id = ws.id AND ($3::uuid IS NULL OR g.environment_id = $3::uuid))::bigint AS replicas,
    (SELECT max(n.at) FROM (
      SELECT DISTINCT ON (d.environment_id) COALESCE(d.started_at, d.created_at) AS at FROM deployments d
      WHERE d.resource_id = ws.id AND ($10::uuid IS NULL OR d.environment_id = $10::uuid)
      ORDER BY d.environment_id, d.created_at DESC, d.id DESC) n) AS deployed_at,
    cpu_millicores(COALESCE(run.target->>'cpu', cfg.target->>'cpu')) AS cpu,
    memory_bytes(COALESCE(run.target->>'memory', cfg.target->>'memory')) AS memory
  FROM ws
  LEFT JOIN cells env ON env.resource_id = ws.id AND env.environment_id = $10::uuid
  LEFT JOIN LATERAL (
    SELECT t.key AS region, t.value AS target FROM jsonb_each(ws.spec->'regions') t
    WHERE t.value->>'enabled' = 'true' ORDER BY t.key LIMIT 1
  ) cfg ON true
  CROSS JOIN LATERAL (
    SELECT COALESCE(env.live_spec->'regions'->cfg.region, env.live_spec->'regions'->env.first_region) AS target
  ) run
),
matched AS (
  SELECT * FROM facts f
  WHERE ($4 = '' OR f.name ILIKE $4 OR f.slug ILIKE $4 OR f.description ILIKE $4
      OR EXISTS (SELECT 1 FROM resource_domains rd WHERE rd.resource_id = f.id AND rd.domain ILIKE $4)
      OR EXISTS (SELECT 1 FROM unnest(f.regions || f.images) v WHERE v ILIKE $4))
    AND ($5 = '' OR $5 = ANY(f.statuses))
    AND (NOT $6 OR cardinality(f.statuses) = 0)
    AND ($7 = '' OR $7 = ANY(f.regions))
    AND ($8 = '' OR f.type = $8)
    AND (NOT $9
      OR EXISTS (SELECT 1 FROM deployments d WHERE d.resource_id = f.id AND d.environment_id = $3::uuid)
      OR NOT EXISTS (SELECT 1 FROM deployments d WHERE d.resource_id = f.id))
)`

// The sort keys of each order as k1, k2 and kt; every order then sorts by name_key and id.
var resourceOrderKeys = map[resourcev1.ResourceOrder]string{
	resourcev1.ResourceOrder_RESOURCE_ORDER_NAME: `0::bigint, 0::bigint, name_key`,
	resourcev1.ResourceOrder_RESOURCE_ORDER_STATUS: `COALESCE((SELECT min(CASE
	    WHEN s IN ('RESOURCE_STATUS_DEGRADED', 'RESOURCE_STATUS_UNAVAILABLE') THEN 0
	    WHEN s IN ('RESOURCE_STATUS_UNKNOWN', 'RESOURCE_STATUS_DEPLOYING') THEN 1
	    WHEN s = 'RESOURCE_STATUS_HEALTHY' THEN 3
	    ELSE 2 END) FROM unnest(sort_statuses) s), 4)::bigint, 0::bigint, ''::text`,
	resourcev1.ResourceOrder_RESOURCE_ORDER_CREATED: `(EXTRACT(EPOCH FROM created_at) * 1000000)::bigint,
	    0::bigint, ''::text`,
	resourcev1.ResourceOrder_RESOURCE_ORDER_SIZE:     `cpu, memory, ''::text`,
	resourcev1.ResourceOrder_RESOURCE_ORDER_REGION:   `0::bigint, 0::bigint, COALESCE(regions[1], '')`,
	resourcev1.ResourceOrder_RESOURCE_ORDER_REPLICAS: `replicas, 0::bigint, ''::text`,
	resourcev1.ResourceOrder_RESOURCE_ORDER_DEPLOYED: `COALESCE((EXTRACT(EPOCH FROM deployed_at) * 1000000)::bigint, 0),
	    0::bigint, ''::text`,
}

var (
	errResourcesToken      = errors.New("invalid resources page token")
	errUnknownResourceType = errors.New("unknown resource type")
)

// resourceFilter holds the parameters of resourceListSQL.
type resourceFilter struct {
	workspaceID       uuid.UUID
	silentSeconds     float64
	environmentID     *uuid.UUID
	pattern           string
	status            string
	notDeployed       bool
	region            string
	resourceType      string
	environmentScoped bool
	sortEnvironmentID *uuid.UUID
	// readable limits the list to these resources; nil lists every resource of the workspace.
	readable []uuid.UUID
}

func (f resourceFilter) args() []any {
	return []any{
		f.workspaceID, f.silentSeconds, f.environmentID, f.pattern, f.status, f.notDeployed,
		f.region, f.resourceType, f.environmentScoped, f.sortEnvironmentID, f.readable,
	}
}

// resourceKey is a row's position in an order; a page token is the last row's key.
type resourceKey struct {
	Order      resourcev1.ResourceOrder `json:"o"`
	Descending bool                     `json:"d"`
	SortEnv    string                   `json:"e,omitempty"`
	K1         int64                    `json:"k1"`
	K2         int64                    `json:"k2"`
	Text       string                   `json:"t"`
	Name       string                   `json:"n"`
	ID         uuid.UUID                `json:"id"`
}

// ListWorkspaceResources lists the resources of a workspace that the request's query and filters
// match, in the request's order.
func (s *ResourceServer) ListWorkspaceResources(
	ctx context.Context,
	req *resourcev1.ListWorkspaceResourcesRequest,
) (*resourcev1.ListWorkspaceResourcesResponse, error) {
	wsID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	readable, err := s.readableResources(ctx, wsID)
	if err != nil {
		return nil, err
	}
	filter, err := s.resourceFilter(ctx, wsID, req)
	if err != nil {
		return nil, err
	}
	filter.readable = readable
	order, descending := req.GetOrderBy(), req.GetDescending()
	if order == resourcev1.ResourceOrder_RESOURCE_ORDER_UNSPECIFIED {
		order, descending = resourcev1.ResourceOrder_RESOURCE_ORDER_CREATED, true
	}
	after, err := decodeResourceKey(req.GetPageToken(), resourceKey{
		Order: order, Descending: descending, SortEnv: req.GetSortEnvironmentId(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
	}
	pageSize := normalizePageSize(req.GetPageSize())
	keys, err := s.resourcePage(ctx, filter, after, pageSize+1)
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	response := &resourcev1.ListWorkspaceResourcesResponse{}
	if len(keys) > int(pageSize) {
		keys = keys[:pageSize]
		last, encodeErr := json.Marshal(keys[len(keys)-1])
		if encodeErr != nil {
			return nil, fmt.Errorf("encode resources page token: %w", encodeErr)
		}
		response.NextPageToken = base64.RawURLEncoding.EncodeToString(last)
	}
	if response.Resources, err = s.listedResources(ctx, keys); err != nil {
		return nil, err
	}
	count := func(ctx context.Context, limit int32) (int64, error) {
		var n int64
		scanErr := s.db.QueryRow(ctx, resourceListSQL+" SELECT count(*) FROM (SELECT 1 FROM matched LIMIT $12) capped",
			append(filter.args(), limit)...).Scan(&n)
		return n, scanErr
	}
	response.TotalCount, err = totalCount(ctx, req.GetIncludeTotalCount(), count)
	if err != nil {
		return nil, fmt.Errorf("count resources: %w", err)
	}
	if req.GetIncludeFacets() {
		if response.Facets, err = s.resourceFacets(ctx, filter); err != nil {
			return nil, err
		}
	}
	return response, nil
}

// readableResources is nil for a caller who can list the workspace's resources, else the
// resources of the workspace GetResource would return to the caller. A caller who can read none of
// them is denied. Without workspace read, only a grant on the resource itself passes GetResource's
// check, so only the caller's resource grants are checked.
func (s *ResourceServer) readableResources(ctx context.Context, wsID uuid.UUID) ([]uuid.UUID, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	denied := check(ctx, s.authz, p, actions.New(actions.ListResources, wsID))
	if denied == nil || connect.CodeOf(denied) != connect.CodePermissionDenied {
		return nil, denied
	}
	var granted []uuid.UUID
	for _, id := range grantedResourceIDs(p.Scopes) {
		checkErr := check(ctx, s.authz, p, actions.New(actions.GetResource, id))
		if checkErr == nil {
			granted = append(granted, id)
		} else if connect.CodeOf(checkErr) != connect.CodePermissionDenied {
			return nil, checkErr
		}
	}
	if len(granted) == 0 {
		return nil, denied
	}
	rows, err := s.queries.ListResourcesByIDs(ctx, granted)
	if err != nil {
		return nil, fmt.Errorf("list the granted resources: %w", err)
	}
	var readable []uuid.UUID
	for _, row := range rows {
		if row.WorkspaceID == wsID {
			readable = append(readable, row.ID)
		}
	}
	if len(readable) == 0 {
		return nil, denied
	}
	return readable, nil
}

// resourceFilter reads the request's filters; environment_id and sort_environment_id must name
// environments of the workspace.
func (s *ResourceServer) resourceFilter(
	ctx context.Context,
	wsID uuid.UUID,
	req *resourcev1.ListWorkspaceResourcesRequest,
) (resourceFilter, error) {
	filter := resourceFilter{
		workspaceID:       wsID,
		silentSeconds:     s.clusterSilentAfter.Seconds(),
		pattern:           likePattern(req.GetQuery()),
		notDeployed:       req.GetNotDeployed(),
		region:            req.GetRegion(),
		environmentScoped: req.GetEnvironmentScoped(),
	}
	if req.GetStatus() != resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED {
		filter.status = req.GetStatus().String()
	}
	if req.GetType() != resourcev1.ResourceType_RESOURCE_TYPE_UNSPECIFIED {
		filter.resourceType = strings.ToLower(strings.TrimPrefix(req.GetType().String(), "RESOURCE_TYPE_"))
	}
	var err error
	if filter.environmentID, err = s.workspaceEnvironment(
		ctx,
		wsID,
		"environment_id",
		req.GetEnvironmentId(),
	); err != nil {
		return resourceFilter{}, err
	}
	filter.sortEnvironmentID = filter.environmentID
	if req.GetSortEnvironmentId() != "" {
		filter.sortEnvironmentID, err = s.workspaceEnvironment(
			ctx,
			wsID,
			"sort_environment_id",
			req.GetSortEnvironmentId(),
		)
		if err != nil {
			return resourceFilter{}, err
		}
	}
	return filter, nil
}

// workspaceEnvironment parses an optional environment ID of the workspace; nil when raw is empty.
func (s *ResourceServer) workspaceEnvironment(
	ctx context.Context,
	wsID uuid.UUID,
	field, raw string,
) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	environmentID, err := parseID(field, raw)
	if err != nil {
		return nil, err
	}
	environment, err := s.queries.GetEnvironmentByID(ctx, environmentID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && environment.WorkspaceID != wsID {
		return nil, ErrEnvironmentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	return &environmentID, nil
}

func (s *ResourceServer) resourcePage(
	ctx context.Context,
	filter resourceFilter,
	after resourceKey,
	limit int32,
) ([]resourceKey, error) {
	args := filter.args()
	sql := resourceListSQL + " SELECT id, name_key, k1, k2, kt FROM (SELECT m.id, m.name_key, x.k1, x.k2, x.kt" +
		" FROM matched m CROSS JOIN LATERAL (SELECT " + resourceOrderKeys[after.Order] + ") x (k1, k2, kt)) k"
	direction, beyond := "ASC", ">"
	if after.Descending {
		direction, beyond = "DESC", "<"
	}
	if after.ID != uuid.Nil {
		sql += " WHERE (k1, k2, kt) " + beyond + " ($12, $13, $14)" +
			" OR ((k1, k2, kt) = ($12, $13, $14) AND (name_key, id) > ($15, $16))"
		args = append(args, after.K1, after.K2, after.Text, after.Name, after.ID)
	}
	sql += fmt.Sprintf(" ORDER BY k1 %[1]s, k2 %[1]s, kt %[1]s, name_key, id LIMIT $%[2]d", direction, len(args)+1)
	rows, err := s.db.Query(ctx, sql, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (resourceKey, error) {
		key := resourceKey{Order: after.Order, Descending: after.Descending, SortEnv: after.SortEnv}
		err := row.Scan(&key.ID, &key.Name, &key.K1, &key.K2, &key.Text)
		return key, err
	})
}

// listedResources loads the resources of keys, in keys' order, with their domains and statuses.
func (s *ResourceServer) listedResources(ctx context.Context, keys []resourceKey) ([]*resourcev1.Resource, error) {
	ids := make([]uuid.UUID, len(keys))
	for i, key := range keys {
		ids[i] = key.ID
	}
	dbResources, err := s.queries.ListResourcesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list resources by ID: %w", err)
	}
	allDomains, err := s.queries.ListResourceDomainsForResources(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list resource domains: %w", err)
	}
	statuses, err := s.environmentStatuses(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("derive resource statuses: %w", err)
	}
	for _, environments := range statuses {
		for _, environment := range environments {
			for _, region := range environment.GetRegions() {
				region.Replicas = nil
			}
		}
	}
	domainsByResource := make(map[uuid.UUID][]genDb.ResourceDomain, len(ids))
	for _, domain := range allDomains {
		domainsByResource[domain.ResourceID] = append(domainsByResource[domain.ResourceID], domain)
	}
	byID := make(map[uuid.UUID]*resourcev1.Resource, len(dbResources))
	for _, dbResource := range dbResources {
		resource, protoErr := s.dbResourceToProto(dbResource, statuses[dbResource.ID], domainsByResource[dbResource.ID])
		if protoErr != nil {
			return nil, protoErr
		}
		byID[dbResource.ID] = resource
	}
	resources := make([]*resourcev1.Resource, 0, len(ids))
	for _, id := range ids {
		// A resource deleted between the two reads drops off this page.
		if resource, ok := byID[id]; ok {
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

// resourceFacets counts the matched resources per status, region and type, each without its own
// filter; the region facet also ignores the status filters and lists every region of the workspace.
func (s *ResourceServer) resourceFacets(
	ctx context.Context,
	filter resourceFilter,
) (*resourcev1.ResourceFacets, error) {
	facets := &resourcev1.ResourceFacets{}
	byStatus := filter
	byStatus.status, byStatus.notDeployed = "", false
	statuses, err := s.facetCounts(ctx, byStatus, ` SELECT s, count(*) FROM matched m CROSS JOIN LATERAL (
	    SELECT DISTINCT s FROM unnest(m.statuses) s WHERE s IS NOT NULL
	    UNION ALL SELECT '' WHERE cardinality(m.statuses) = 0) x (s)
	  GROUP BY s ORDER BY s`)
	if err != nil {
		return nil, fmt.Errorf("count resources by status: %w", err)
	}
	for _, c := range statuses {
		if c.value == "" {
			facets.NotDeployed = c.count
			continue
		}
		status, parseErr := parseResourceStatus(c.value)
		if parseErr != nil {
			return nil, parseErr
		}
		facets.Statuses = append(facets.Statuses, &resourcev1.ResourceStatusCount{Status: status, Count: c.count})
	}
	byRegion := byStatus
	byRegion.region = ""
	regions, err := s.facetCounts(
		ctx,
		byRegion,
		` SELECT g, count(m.id) FROM (SELECT DISTINCT unnest(regions) g FROM facts) a
	  LEFT JOIN matched m ON g = ANY(m.regions) GROUP BY g ORDER BY g`,
	)
	if err != nil {
		return nil, fmt.Errorf("count resources by region: %w", err)
	}
	for _, c := range regions {
		facets.Regions = append(facets.Regions, &resourcev1.ResourceRegionCount{Region: c.value, Count: c.count})
	}
	byType := filter
	byType.resourceType = ""
	types, err := s.facetCounts(ctx, byType, " SELECT type, count(*) FROM matched GROUP BY type ORDER BY type")
	if err != nil {
		return nil, fmt.Errorf("count resources by type: %w", err)
	}
	for _, c := range types {
		value, ok := resourcev1.ResourceType_value["RESOURCE_TYPE_"+strings.ToUpper(c.value)]
		if !ok {
			return nil, fmt.Errorf("%w: %q", errUnknownResourceType, c.value)
		}
		facets.Types = append(facets.Types, &resourcev1.ResourceTypeCount{
			Type: resourcev1.ResourceType(value), Count: c.count,
		})
	}
	return facets, nil
}

type facetCount struct {
	value string
	count int32
}

func (s *ResourceServer) facetCounts(ctx context.Context, filter resourceFilter, query string) ([]facetCount, error) {
	rows, err := s.db.Query(ctx, resourceListSQL+query, filter.args()...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (facetCount, error) {
		var value string
		var n int64
		err := row.Scan(&value, &n)
		return facetCount{value: value, count: cappedCount(n)}, err
	})
}

// decodeResourceKey reads a page token of key's order, direction and sort environment; the first
// page's empty token is key itself.
func decodeResourceKey(token string, key resourceKey) (resourceKey, error) {
	if token == "" {
		return key, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return key, errResourcesToken
	}
	var decoded resourceKey
	if err = json.Unmarshal(raw, &decoded); err != nil || decoded.ID == uuid.Nil ||
		decoded.Order != key.Order || decoded.Descending != key.Descending || decoded.SortEnv != key.SortEnv {
		return key, errResourcesToken
	}
	return decoded, nil
}
