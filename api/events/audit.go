package events

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apiDb "github.com/team-loco/loco/api/db"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/internal/logquery"
)

// AuditQuery selects events within optional scope and time bounds: an organization's, or with no
// OrgID every event, or with InstallationOnly the ones that belong to no organization.
type AuditQuery struct {
	OrgID            uuid.UUID
	InstallationOnly bool
	WorkspaceID      *uuid.UUID
	ResourceID       *uuid.UUID
	// ResourceIDs, when set, keeps events of these resources.
	ResourceIDs []uuid.UUID
	// ResourcesOnly keeps events of resources currently in the event's workspace.
	ResourcesOnly bool
	EnvironmentID *uuid.UUID
	Start, End    *time.Time
	// BeforeSeq, when positive, selects events with a lower seq.
	BeforeSeq     int64
	AfterPosition *int64
	Stream        bool
	// Types, when set, keeps events of these types.
	Types  []string
	Filter logquery.Expr
	Limit  int32
}

// AuditEvent is an event with the email and name of the user who acted, when one did. The name of
// an API token that acted is ActorName and the entity type that owns it is ActorTokenType.
type AuditEvent struct {
	genDb.Event
	ActorEmail *string
	ActorName  *string
	// Title, Severity and Region are the dashboard's labels for the event, from event_title,
	// event_severity and event_region.
	Title    string
	Severity string
	Region   string
	// OrgName is the organization's name, or the name an event about a deleted organization recorded.
	OrgName string
}

const auditSelect = `SELECT e.seq, e.stream_position, e.id, e.type, e.org_id, e.workspace_id, e.resource_id,
	e.environment_id, e.deployment_id, e.actor_type, e.actor_id, e.subject_type, e.subject_id, e.request_id,
	e.traceparent, e.data, e.created_at, e.acted_as_operator,
	u.email, COALESCE(u.name, e.actor_token_name), e.actor_token_type,
	event_title(e.type, e.data), event_severity(e.type), event_region(e.data),
	COALESCE(o.name, CASE WHEN e.subject_type = 'organization' THEN e.data->>'name' END, '')` + auditFrom

const auditFrom = `
FROM events e
LEFT JOIN users u ON e.actor_type = 'user' AND u.id = e.actor_id
LEFT JOIN organizations o ON o.id = e.org_id
LEFT JOIN users s ON e.subject_type = 'user' AND s.id = e.subject_id
LEFT JOIN resources r ON r.id = e.resource_id
LEFT JOIN workspaces w ON w.id = e.workspace_id
LEFT JOIN environments n ON n.id = e.environment_id
WHERE true`

// ListAudit reads the events q selects.
func ListAudit(ctx context.Context, db genDb.DBTX, q AuditQuery) ([]AuditEvent, error) {
	sql, args := auditSQL(q)
	rows, err := db.Query(ctx, apiDb.Named("ListAuditEvents", sql), args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (AuditEvent, error) {
		var event AuditEvent
		err := row.Scan(
			&event.Seq,
			&event.StreamPosition,
			&event.ID,
			&event.Type,
			&event.OrgID,
			&event.WorkspaceID,
			&event.ResourceID,
			&event.EnvironmentID,
			&event.DeploymentID,
			&event.ActorType,
			&event.ActorID,
			&event.SubjectType,
			&event.SubjectID,
			&event.RequestID,
			&event.Traceparent,
			&event.Data,
			&event.CreatedAt,
			&event.ActedAsOperator,
			&event.ActorEmail,
			&event.ActorName,
			&event.ActorTokenType,
			&event.Title,
			&event.Severity,
			&event.Region,
			&event.OrgName,
		)
		return event, err
	})
}

// CountAudit counts the events q selects, at most q.Limit of them.
func CountAudit(ctx context.Context, db genDb.DBTX, q AuditQuery) (int64, error) {
	b := &binder{}
	sql := "SELECT count(*) FROM (SELECT 1" + auditFrom + auditWhere(q, b) + " LIMIT " + b.bind(q.Limit) + ") capped"
	var n int64
	err := db.QueryRow(ctx, apiDb.Named("CountAuditEvents", sql), b.args...).Scan(&n)
	return n, err
}

func auditSQL(q AuditQuery) (string, []any) {
	b := &binder{}
	sql := auditSelect + auditWhere(q, b)
	if q.Stream {
		sql += " ORDER BY e.stream_position"
	} else {
		sql += " ORDER BY e.seq DESC"
	}
	sql += " LIMIT " + b.bind(q.Limit)
	return sql, b.args
}

// auditWhere is the conditions after auditFrom's WHERE.
func auditWhere(q AuditQuery, b *binder) string {
	sql := ""
	if q.OrgID != uuid.Nil {
		sql += " AND e.org_id = " + b.bind(q.OrgID)
	} else if q.InstallationOnly {
		sql += " AND e.org_id IS NULL"
	}
	if len(q.Types) > 0 {
		sql += " AND e.type = ANY(" + b.bind(q.Types) + ")"
	}
	if q.WorkspaceID != nil {
		sql += " AND e.workspace_id = " + b.bind(*q.WorkspaceID)
	}
	if q.ResourceID != nil {
		sql += " AND e.resource_id = " + b.bind(*q.ResourceID)
	}
	if q.ResourceIDs != nil {
		sql += " AND e.resource_id = ANY(" + b.bind(q.ResourceIDs) + ")"
	}
	if q.ResourcesOnly {
		sql += " AND r.workspace_id = e.workspace_id"
	}
	if q.EnvironmentID != nil {
		sql += " AND e.environment_id = " + b.bind(*q.EnvironmentID)
	}
	if q.Start != nil {
		sql += " AND e.created_at >= " + b.bind(*q.Start)
	}
	if q.End != nil {
		sql += " AND e.created_at < " + b.bind(*q.End)
	}
	if q.BeforeSeq > 0 {
		sql += " AND e.seq < " + b.bind(q.BeforeSeq)
	}
	if q.Stream || q.AfterPosition != nil {
		var position int64
		if q.AfterPosition != nil {
			position = *q.AfterPosition
		}
		sql += " AND e.stream_position > " + b.bind(position)
	}
	if q.Filter != nil {
		sql += " AND " + querySQL(q.Filter, b)
	}
	return sql
}

// Facet is the events per value of one key.
type Facet struct {
	Key, Value string
	Count      int64
}

// facetValues are the facet keys over the subquery e of FacetAudit.
const facetValues = `(VALUES ('type', e.type), ('severity', event_severity(e.type)), ('region', event_region(e.data)),
	('reason', COALESCE(e.data->>'reason', '')),
	('actor', CASE WHEN e.actor_type = 'system' THEN 'system' ELSE COALESCE(e.user_name, e.actor_token_name, '') END),
	('resource', COALESCE(e.resource_name, '')))`

// FacetAudit counts each facet key's values over the newest q.Limit events q selects, ignoring q.Filter.
func FacetAudit(ctx context.Context, db genDb.DBTX, q AuditQuery) ([]Facet, error) {
	q.Filter = nil
	b := &binder{}
	sql := "SELECT f.key, f.value, count(*) FROM (SELECT e.type, e.data, e.actor_type, e.actor_token_name, " +
		"u.name AS user_name, r.name AS resource_name" + auditFrom + auditWhere(q, b) +
		" ORDER BY e.seq DESC LIMIT " + b.bind(q.Limit) + ") e" +
		" CROSS JOIN LATERAL " + facetValues + " f(key, value) WHERE f.value <> '' GROUP BY f.key, f.value" +
		" ORDER BY f.key, count(*) DESC, f.value"
	rows, err := db.Query(ctx, apiDb.Named("CountAuditEventFacets", sql), b.args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Facet, error) {
		var f Facet
		err := row.Scan(&f.Key, &f.Value, &f.Count)
		return f, err
	})
}
