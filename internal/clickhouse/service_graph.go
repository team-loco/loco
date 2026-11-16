package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

func (c *Client) QueryServiceGraph(
	ctx context.Context,
	req *observabilityv1.QueryServiceGraphRequest,
	resources []ResourceName,
) (*observabilityv1.QueryServiceGraphResponse, error) {
	query, args := serviceGraphSQL(req, resources, c.queryTimeout)
	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query service graph: %w", err)
	}
	defer rows.Close()
	response := &observabilityv1.QueryServiceGraphResponse{}
	for rows.Next() {
		edge := &observabilityv1.ServiceGraphEdge{}
		if err = rows.Scan(&edge.From, &edge.To, &edge.Requests, &edge.Errors, &edge.P95Ms); err != nil {
			return nil, fmt.Errorf("scan service graph edge: %w", err)
		}
		response.Edges = append(response.Edges, edge)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("service graph rows: %w", err)
	}
	return response, nil
}

func serviceGraphSQL(
	req *observabilityv1.QueryServiceGraphRequest,
	resources []ResourceName,
	timeout time.Duration,
) (string, []any) {
	b := &positionalBinder{}
	scope := func() string {
		return strings.Join(
			traceScope(
				&observabilityv1.TraceScope{WorkspaceId: req.GetWorkspaceId(), EnvironmentId: req.GetEnvironmentId()},
				b,
			),
			" AND ",
		)
	}
	clients := "SELECT TraceId, SpanId, any(ResourceId) AS resource, any(ServiceName) AS service, " +
		"any(SpanAttributes) AS attributes, max(Duration) AS duration, max(StatusCode = 'Error') AS failed " +
		"FROM otel_traces WHERE " + scope() + " AND SpanKind = 'Client' AND Timestamp >= " +
		"fromUnixTimestamp64Nano(" + b.num(req.GetStartTime().AsTime().UnixNano()) + ") AND Timestamp <= " +
		"fromUnixTimestamp64Nano(" + b.num(req.GetEndTime().AsTime().UnixNano()) + ")" +
		" GROUP BY TraceId, SpanId"
	// Server spans can start outside the client time range.
	servers := "SELECT TraceId, ParentSpanId, any(ResourceId) AS resource, any(ServiceName) AS service, " +
		"max(StatusCode = 'Error') AS failed FROM otel_traces WHERE " + scope() +
		" AND SpanKind = 'Server' AND TraceId IN (SELECT TraceId FROM clients) GROUP BY TraceId, ParentSpanId"
	source := serviceGraphName("c.resource", "c.service", resources, b)
	target := serviceGraphName("s.resource", "s.service", resources, b)
	query := "WITH clients AS (" + clients + "), servers AS (" + servers + ") " +
		"SELECT source, target, count(), countIf(failed), quantile(0.95)(duration) / 1e6 FROM (" +
		"SELECT " + source + " AS source, coalesce(nullIf(" + target + ", ''), " +
		"nullIf(trimBoth(c.attributes['peer.service']), ''), trimBoth(c.attributes['db.system'])) AS target, " +
		"c.duration AS duration, c.failed OR s.failed AS failed FROM clients c LEFT JOIN servers s " +
		"ON c.TraceId = s.TraceId AND c.SpanId = s.ParentSpanId) " +
		"WHERE source != '' AND target != '' GROUP BY source, target ORDER BY source, target" +
		fmt.Sprintf(" SETTINGS max_execution_time = %d", int(timeout.Seconds()))
	return query, b.args
}

func serviceGraphName(resource, service string, resources []ResourceName, b binder) string {
	if len(resources) == 0 {
		return service
	}
	ids, names := make([]string, len(resources)), make([]string, len(resources))
	for i, r := range resources {
		ids[i] = b.str(r.ID)
	}
	for i, r := range resources {
		names[i] = b.str(r.Name)
	}
	return "transform(" + resource + ", [" + strings.Join(
		ids,
		", ",
	) + "], [" + strings.Join(
		names,
		", ",
	) + "], " + service + ")"
}
