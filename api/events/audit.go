package events

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/internal/logquery"
)

// AuditQuery selects an organization's events within optional scope and time bounds.
type AuditQuery struct {
	OrgID         uuid.UUID
	WorkspaceID   *uuid.UUID
	ResourceID    *uuid.UUID
	EnvironmentID *uuid.UUID
	Start, End    *time.Time
	// BeforeSeq, when positive, selects events with a lower seq.
	BeforeSeq     int64
	AfterPosition int64
	Stream        bool
	Filter        logquery.Expr
	Limit         int32
}

// AuditEvent is an event with the email and name of the user who acted, when one did. The name of
// an API token that acted is ActorName and the entity type that owns it is ActorTokenType.
type AuditEvent struct {
	genDb.Event
	ActorEmail *string
	ActorName  *string
}

const auditSelect = `SELECT e.seq, e.stream_position, e.id, e.type, e.org_id, e.workspace_id, e.resource_id,
	e.environment_id, e.deployment_id, e.actor_type, e.actor_id, e.subject_type, e.subject_id, e.request_id,
	e.traceparent, e.data, e.created_at,
	u.email, COALESCE(u.name, e.actor_token_name), e.actor_token_type
FROM events e
LEFT JOIN users u ON e.actor_type = 'user' AND u.id = e.actor_id
LEFT JOIN users s ON e.subject_type = 'user' AND s.id = e.subject_id
LEFT JOIN resources r ON r.id = e.resource_id
LEFT JOIN workspaces w ON w.id = e.workspace_id
LEFT JOIN environments n ON n.id = e.environment_id
WHERE e.org_id = $1`

// ListAudit reads the events q selects.
func ListAudit(ctx context.Context, db genDb.DBTX, q AuditQuery) ([]AuditEvent, error) {
	sql, args := auditSQL(q)
	rows, err := db.Query(ctx, sql, args...)
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
			&event.ActorEmail,
			&event.ActorName,
			&event.ActorTokenType,
		)
		return event, err
	})
}

// CountAudit counts the events q selects, at most q.Limit of them.
func CountAudit(ctx context.Context, db genDb.DBTX, q AuditQuery) (int64, error) {
	sql, args := auditSQL(q)
	var n int64
	err := db.QueryRow(ctx, "SELECT count(*) FROM ("+sql+") capped", args...).Scan(&n)
	return n, err
}

func auditSQL(q AuditQuery) (string, []any) {
	b := &binder{args: []any{q.OrgID}}
	sql := auditSelect
	if q.WorkspaceID != nil {
		sql += " AND e.workspace_id = " + b.bind(*q.WorkspaceID)
	}
	if q.ResourceID != nil {
		sql += " AND e.resource_id = " + b.bind(*q.ResourceID)
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
	if q.Filter != nil {
		sql += " AND " + querySQL(q.Filter, b)
	}
	if q.Stream {
		sql += " AND e.stream_position > " + b.bind(q.AfterPosition) + " ORDER BY e.stream_position"
	} else {
		sql += " ORDER BY e.seq DESC"
	}
	sql += " LIMIT " + b.bind(q.Limit)
	return sql, b.args
}
