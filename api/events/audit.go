package events

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/internal/logquery"
)

// AuditQuery selects a page of an organization's events, newest first. Nil scope fields read
// every workspace, resource or environment; Filter is a validated audit query or nil.
type AuditQuery struct {
	OrgID         uuid.UUID
	WorkspaceID   *uuid.UUID
	ResourceID    *uuid.UUID
	EnvironmentID *uuid.UUID
	Start, End    *time.Time
	BeforeSeq     int64
	Filter        logquery.Expr
	Limit         int32
}

// AuditEvent is an event with the email and name of the user who acted, when one did.
type AuditEvent struct {
	genDb.Event
	ActorEmail *string
	ActorName  *string
}

const auditSelect = `SELECT e.seq, e.txid, e.id, e.type, e.org_id, e.workspace_id, e.resource_id, e.environment_id,
	e.actor_type, e.actor_id, e.subject_type, e.subject_id, e.request_id, e.traceparent, e.data, e.created_at,
	u.email, u.name
FROM events e
LEFT JOIN users u ON e.actor_type = 'user' AND u.id = e.actor_id
LEFT JOIN users s ON e.subject_type = 'user' AND s.id = e.subject_id
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
			&event.Seq, &event.Txid, &event.ID, &event.Type, &event.OrgID, &event.WorkspaceID, &event.ResourceID,
			&event.EnvironmentID, &event.ActorType, &event.ActorID, &event.SubjectType, &event.SubjectID,
			&event.RequestID, &event.Traceparent, &event.Data, &event.CreatedAt, &event.ActorEmail, &event.ActorName,
		)
		return event, err
	})
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
	sql += " ORDER BY e.seq DESC LIMIT " + b.bind(q.Limit)
	return sql, b.args
}
