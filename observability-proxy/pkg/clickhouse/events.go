package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const eventsTable = "otel_k8s_events"

type EventQuery struct {
	WorkspaceID   string
	EnvironmentID string
	ResourceIDs   []string
	Start         time.Time
	End           time.Time
	Limit         int32
}

func QueryEvents(
	ctx context.Context,
	conn driver.Conn,
	q EventQuery,
	queryTimeout int,
) ([]*observabilityv1.KubernetesEvent, error) {
	query, args := buildEventsQuery(q, queryTimeout)
	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse events query: %w", err)
	}
	defer rows.Close()

	var events []*observabilityv1.KubernetesEvent
	for rows.Next() {
		var (
			ts         time.Time
			eventType  string
			reason     string
			message    string
			objectKind string
			objectName string
			count      uint64
			resourceID string
		)
		if err := rows.Scan(
			&ts,
			&eventType,
			&reason,
			&message,
			&objectKind,
			&objectName,
			&count,
			&resourceID,
		); err != nil {
			return nil, fmt.Errorf("scan event row: %w", err)
		}
		lastSeen := timestamppb.New(ts)
		events = append(events, &observabilityv1.KubernetesEvent{
			Timestamp:  lastSeen,
			Type:       eventType,
			Reason:     reason,
			Message:    message,
			ObjectKind: objectKind,
			ObjectName: objectName,
			Count:      int64(count),
			ResourceId: resourceID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}
	return events, nil
}

func buildEventsQuery(q EventQuery, queryTimeout int) (string, []any) {
	whereParts := []string{workspaceColumn + " = ?"}
	args := []any{q.WorkspaceID}

	if q.EnvironmentID != "" {
		whereParts = append(whereParts, environmentColumn+" = ?")
		args = append(args, q.EnvironmentID)
	}

	if len(q.ResourceIDs) > 0 {
		clause, values := inFilter(resourceColumn, q.ResourceIDs)
		whereParts = append(whereParts, clause)
		args = append(args, values...)
	}

	whereParts = append(
		whereParts,
		"Timestamp >= fromUnixTimestamp64Nano(?)",
		"Timestamp <= fromUnixTimestamp64Nano(?)",
	)
	args = append(args, q.Start.UnixNano(), q.End.UnixNano(), q.Limit)

	query := fmt.Sprintf(
		"SELECT max(Timestamp) AS LastSeen, argMax(EventType, Timestamp), argMax(Reason, Timestamp), "+
			"argMax(Message, Timestamp), argMax(ObjectKind, Timestamp), argMax(ObjectName, Timestamp), "+
			"max(Count), argMax(%s, Timestamp) FROM %s WHERE %s GROUP BY EventUid "+
			"ORDER BY LastSeen DESC LIMIT ? SETTINGS max_execution_time = %d",
		resourceColumn,
		eventsTable,
		strings.Join(whereParts, " AND "),
		queryTimeout,
	)
	return query, args
}
