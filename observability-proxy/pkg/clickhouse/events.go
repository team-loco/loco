package clickhouse

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

var ErrInvalidEventCursor = errors.New("invalid workload event cursor")

const (
	eventCursorVersion  = 1
	maxEventCursorBytes = 4096
)

type EventQueryOptions struct {
	Limit     int32
	Timeout   int
	Retention time.Duration
	CursorTTL time.Duration
	Now       time.Time
}

type eventCursor struct {
	Version int    `json:"v"`
	Created int64  `json:"c"`
	ID      string `json:"id"`
	Filter  string `json:"f"`
	Issued  int64  `json:"i"`
}

type eventMessage struct {
	reasons []string
	code    string
	summary string
}

var workloadEventMessages = []eventMessage{
	{
		[]string{"FailedCreate", "Failed", "CreateContainerConfigError", "CreateContainerError"},
		"start_failed",
		"Application could not start. Check its configuration and available capacity.",
	},
	{[]string{"FailedScheduling"}, "capacity_unavailable", "Application is waiting for available capacity."},
	{
		[]string{"ErrImagePull", "ImagePullBackOff", "InvalidImageName", "FailedToRetrieveImagePullSecret"},
		"image_unavailable",
		"Application image could not be loaded.",
	},
	{[]string{"BackOff"}, "retry_delayed", "Application is waiting before retrying."},
	{[]string{"CrashLoopBackOff"}, "restart_delayed", "Application is restarting after an unsuccessful start."},
	{[]string{"Unhealthy", "ProbeWarning"}, "health_check_failed", "Application did not pass a health check."},
	{[]string{"Started"}, "started", "Application started."},
	{[]string{"Killing"}, "stopping", "Application is stopping."},
	{[]string{"Pulled", "Pulling"}, "image_loading", "Application image is being prepared."},
	{[]string{"ScalingReplicaSet", "SuccessfulCreate", "Scheduled", "Created"}, "starting", "Application is starting."},
}

func eventMessageSQL(args *[]any) string {
	branches := make([]string, 0, len(workloadEventMessages)*3+1)
	for _, mapping := range workloadEventMessages {
		placeholders := make([]string, len(mapping.reasons))
		for index, reason := range mapping.reasons {
			placeholders[index] = "?"
			*args = append(*args, reason)
		}
		branches = append(branches, "Reason IN ("+strings.Join(placeholders, ",")+")", "tuple(?, ?)")
		*args = append(*args, mapping.code, mapping.summary)
	}
	branches = append(branches, "if(NativeType = 'Warning', tuple(?, ?), tuple(?, ?))")
	*args = append(*args, "warning", "Application reported a warning.", "status_changed", "Application status changed.")
	return "multiIf(" + strings.Join(branches, ",") + ")"
}

func eventFilterFingerprint(req *observabilityv1.QueryEventsRequest) (string, error) {
	resources := append([]string{}, req.GetResourceIds()...)
	slices.Sort(resources)
	resources = slices.Compact(resources)
	severities := append([]observabilityv1.WorkloadEventSeverity{}, req.GetSeverities()...)
	slices.Sort(severities)
	severities = slices.Compact(severities)
	filter := struct {
		Workspace   string                                  `json:"workspace"`
		Resources   []string                                `json:"resources"`
		Environment string                                  `json:"environment"`
		Start       int64                                   `json:"start"`
		End         int64                                   `json:"end"`
		Region      string                                  `json:"region"`
		Search      string                                  `json:"search"`
		Severities  []observabilityv1.WorkloadEventSeverity `json:"severities"`
	}{
		Workspace: req.GetWorkspaceId(), Resources: resources, Environment: req.GetEnvironmentId(),
		Start: req.GetStartTime().AsTime().UnixNano(), End: req.GetEndTime().AsTime().UnixNano(),
		Region: req.GetRegion(), Search: req.GetSearch(), Severities: severities,
	}
	encoded, err := json.Marshal(filter)
	if err != nil {
		return "", fmt.Errorf("encode event filters: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func decodeEventCursor(raw, fingerprint string, opts EventQueryOptions) (eventCursor, error) {
	var position eventCursor
	if len(raw) > maxEventCursorBytes {
		return position, ErrInvalidEventCursor
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return position, ErrInvalidEventCursor
	}
	if decodeErr := json.Unmarshal(encoded, &position); decodeErr != nil {
		return position, ErrInvalidEventCursor
	}
	id, err := hex.DecodeString(position.ID)
	if err != nil || len(id) != sha256.Size || position.ID != strings.ToLower(position.ID) ||
		position.Version != eventCursorVersion ||
		position.Filter != fingerprint ||
		position.Issued > opts.Now.UnixNano() ||
		opts.Now.Sub(time.Unix(0, position.Issued)) > opts.CursorTTL {
		return position, ErrInvalidEventCursor
	}
	return position, nil
}

func (position eventCursor) encode() (string, error) {
	encoded, err := json.Marshal(position)
	if err != nil {
		return "", fmt.Errorf("encode event cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

const eventOwnershipSQL = `WITH
owner_rows AS (
 SELECT ClusterUid, ObjectUid, ObjectKind, ObjectNamespace, ResourceVersion, ApplicationUid,
  OrganizationId, WorkspaceId, EnvironmentId, ResourceId, ResourceName, OwnerUids, OwnerKinds, OwnerApiVersions,
  Timestamp, Body, JSONExtractString(Body, 'apiVersion') AS ApiVersion,
  multiIf(
   ObjectKind = 'Application', ApiVersion = 'infra.loco.io/v1alpha1' AND ApplicationUid = ObjectUid AND
   notEmpty(OrganizationId) AND notEmpty(WorkspaceId) AND notEmpty(ResourceId),
   ObjectKind = 'Deployment', ApiVersion = 'apps/v1' AND notEmpty(ApplicationUid) AND notEmpty(EnvironmentId),
   ObjectKind = 'ReplicaSet', ApiVersion = 'apps/v1' AND length(OwnerUids) = 1 AND notEmpty(OwnerUids[1]) AND
   OwnerKinds[1] = 'Deployment' AND OwnerApiVersions[1] = 'apps/v1',
   ObjectKind = 'Pod', ApiVersion = 'v1' AND length(OwnerUids) = 1 AND notEmpty(OwnerUids[1]) AND OwnerKinds[1] =
   'ReplicaSet' AND OwnerApiVersions[1] = 'apps/v1',
   false
  ) AS Complete,
  tuple(ObjectKind, ObjectNamespace, ApiVersion,
   if(ObjectKind = 'Application', tuple(OrganizationId, WorkspaceId, ResourceId), tuple('', '', '')),
   if(ObjectKind = 'Deployment', tuple(ApplicationUid, EnvironmentId), tuple('', '')),
   if(ObjectKind IN ('Pod', 'ReplicaSet'), tuple(OwnerUids, OwnerKinds, OwnerApiVersions), tuple([], [], []))
  ) AS Identity
 FROM otel_workload_owners
 WHERE notEmpty(ClusterUid) AND notEmpty(ObjectUid) AND notEmpty(ObjectNamespace)
  AND toInt128(toUnixTimestamp64Nano(Timestamp)) >= toInt128(?) - toInt128(?) - toInt128(?)
),
owner_states AS (
 SELECT ClusterUid, ObjectUid,
  argMax(tuple(ObjectKind, ObjectNamespace, ApiVersion, ApplicationUid, OrganizationId, WorkspaceId, EnvironmentId,
   ResourceId, ResourceName, OwnerUids, OwnerKinds, OwnerApiVersions, Timestamp, Complete), tuple(Timestamp,
   cityHash64(Body))) AS State,
  uniqExactIf(Identity, Complete) AS Identities
 FROM owner_rows GROUP BY ClusterUid, ObjectUid
),
owners AS (
 SELECT ClusterUid, ObjectUid, State.1 AS Kind, State.2 AS Namespace, State.4 AS AppUid,
  State.5 AS OrganizationId, State.6 AS WorkspaceId, State.7 AS EnvironmentId, State.8 AS ResourceId,
  State.9 AS ResourceName, State.10[1] AS ParentUid, State.13 AS ObservedAt
 FROM owner_states WHERE Identities = 1 AND State.14
),
scoped_apps AS (
 SELECT * FROM owners WHERE Kind = 'Application' AND WorkspaceId = ?
),
deployments AS (
 SELECT d.ClusterUid AS ClusterUid, d.ObjectUid AS ObjectUid, d.Namespace AS Namespace, a.WorkspaceId AS
   WorkspaceId, a.ResourceId AS ResourceId, a.ResourceName AS ResourceName,
  d.EnvironmentId AS EnvironmentId, least(a.ObservedAt, d.ObservedAt) AS OldestOwner
 FROM owners d INNER JOIN scoped_apps a ON d.ClusterUid = a.ClusterUid AND d.AppUid = a.ObjectUid
 WHERE d.Kind = 'Deployment' AND d.Namespace = concat('ws-', a.WorkspaceId)
),
replicasets AS (
 SELECT r.ClusterUid AS ClusterUid, r.ObjectUid AS ObjectUid, r.Namespace AS Namespace, d.WorkspaceId AS
   WorkspaceId, d.ResourceId AS ResourceId, d.ResourceName AS ResourceName,
  d.EnvironmentId AS EnvironmentId, least(d.OldestOwner, r.ObservedAt) AS OldestOwner
 FROM owners r INNER JOIN deployments d ON r.ClusterUid = d.ClusterUid AND r.ParentUid = d.ObjectUid
 WHERE r.Kind = 'ReplicaSet' AND r.Namespace = d.Namespace
),
pods AS (
 SELECT p.ClusterUid AS ClusterUid, p.ObjectUid AS ObjectUid, p.Namespace AS Namespace, r.WorkspaceId AS
   WorkspaceId, r.ResourceId AS ResourceId, r.ResourceName AS ResourceName,
  r.EnvironmentId AS EnvironmentId, least(r.OldestOwner, p.ObservedAt) AS OldestOwner
 FROM owners p INNER JOIN replicasets r ON p.ClusterUid = r.ClusterUid AND p.ParentUid = r.ObjectUid
 WHERE p.Kind = 'Pod' AND p.Namespace = r.Namespace
),
subjects AS (
 SELECT *, 'Deployment' AS Kind FROM deployments
 UNION ALL SELECT *, 'ReplicaSet' AS Kind FROM replicasets
 UNION ALL SELECT *, 'Pod' AS Kind FROM pods
),
event_states AS (
 SELECT ClusterUid, ObjectUid,
  argMax(tuple(SubjectUid, SubjectKind, SubjectNamespace, CreatedAt, FirstSeen, LastSeen, Occurrences, Region,
   Timestamp, JSONExtractString(Body, 'type'), JSONExtractString(Body, 'reason')),
   tuple(LastSeen, JSONHas(Body, 'series', 'count'), Occurrences, Timestamp, cityHash64(Body))) AS State,
  uniqExact(tuple(ObjectNamespace, SubjectUid, SubjectKind, SubjectNamespace, JSONExtractString(Body, 'regarding',
   'apiVersion'))) AS Identities
 FROM otel_workload_events
 WHERE notEmpty(ClusterUid) AND notEmpty(ObjectUid) AND ObjectKind = 'Event'
  AND toInt128(toUnixTimestamp64Nano(Timestamp)) >= toInt128(?) - toInt128(?)
  AND ObjectNamespace = SubjectNamespace
  AND (SubjectKind = 'Pod' AND JSONExtractString(Body, 'regarding', 'apiVersion') = 'v1'
   OR SubjectKind IN ('Deployment', 'ReplicaSet') AND JSONExtractString(Body, 'regarding', 'apiVersion') =
   'apps/v1')
  AND CreatedAt > fromUnixTimestamp64Nano(0) AND FirstSeen > fromUnixTimestamp64Nano(0)
  AND LastSeen <= fromUnixTimestamp64Nano(?) AND Occurrences > 0
 GROUP BY ClusterUid, ObjectUid
),
attributed AS (
 SELECT lower(hex(SHA256(toJSONString(tuple(e.ClusterUid, e.ObjectUid))))) AS Id,
  e.State.4 AS CreatedAt, e.State.5 AS FirstSeen, e.State.6 AS LastSeen, e.State.7 AS Occurrences,
  e.State.8 AS Region, e.State.10 AS NativeType, e.State.11 AS Reason,
  s.ResourceId AS ResourceId, s.ResourceName AS ResourceName, s.EnvironmentId AS EnvironmentId
 FROM event_states e INNER JOIN subjects s
  ON e.ClusterUid = s.ClusterUid AND e.State.1 = s.ObjectUid AND e.State.2 = s.Kind AND e.State.3 = s.Namespace
 WHERE e.Identities = 1 AND e.State.6 >= fromUnixTimestamp64Nano(?)
  AND toInt128(toUnixTimestamp64Nano(s.OldestOwner)) >= toInt128(toUnixTimestamp64Nano(e.State.9)) - toInt128(?)
)
`

func buildEventsQuery(
	req *observabilityv1.QueryEventsRequest,
	opts EventQueryOptions,
) (string, []any, eventCursor, error) {
	fingerprint, fingerprintErr := eventFilterFingerprint(req)
	if fingerprintErr != nil {
		return "", nil, eventCursor{}, fingerprintErr
	}
	position := eventCursor{Version: eventCursorVersion, Filter: fingerprint, Issued: opts.Now.UnixNano()}
	if req.GetCursor() != "" {
		var err error
		position, err = decodeEventCursor(req.GetCursor(), fingerprint, opts)
		if err != nil {
			return "", nil, eventCursor{}, err
		}
	}
	args := []any{
		opts.Now.UnixNano(),
		opts.Retention.Nanoseconds(),
		opts.Retention.Nanoseconds(),
		req.GetWorkspaceId(),
		opts.Now.UnixNano(),
		opts.Retention.Nanoseconds(),
		req.GetEndTime().AsTime().UnixNano(),
		req.GetStartTime().AsTime().UnixNano(),
		opts.Retention.Nanoseconds(),
	}
	message := eventMessageSQL(&args)
	severity := fmt.Sprintf(
		"if(NativeType = 'Warning', %d, %d)",
		observabilityv1.WorkloadEventSeverity_WORKLOAD_EVENT_SEVERITY_WARNING,
		observabilityv1.WorkloadEventSeverity_WORKLOAD_EVENT_SEVERITY_INFO,
	)
	query := eventOwnershipSQL + ", safe AS (SELECT *, " + message + " AS Message, " + severity +
		" AS Severity FROM attributed WHERE NativeType IN ('Normal', 'Warning'))" +
		" SELECT Id, CreatedAt, ResourceId, ResourceName, EnvironmentId, Region, Severity," +
		" Message.1, Message.2, Occurrences, FirstSeen, LastSeen FROM safe WHERE Region = ?"
	args = append(args, req.GetRegion())
	if req.GetEnvironmentId() != "" {
		query += " AND EnvironmentId = ?"
		args = append(args, req.GetEnvironmentId())
	}
	if len(req.GetResourceIds()) > 0 {
		clause, values := inFilter("ResourceId", req.GetResourceIds())
		query += " AND " + clause
		args = append(args, values...)
	}
	if len(req.GetSeverities()) > 0 {
		placeholders := make([]string, len(req.GetSeverities()))
		for index, value := range req.GetSeverities() {
			placeholders[index] = "?"
			args = append(args, int32(value))
		}
		query += " AND Severity IN (" + strings.Join(placeholders, ",") + ")"
	}
	if req.GetSearch() != "" {
		query += " AND positionUTF8(concat(ResourceName, ' ', Message.2), ?) > 0"
		args = append(args, req.GetSearch())
	}
	if req.GetCursor() != "" {
		query += " AND (CreatedAt, Id) < (fromUnixTimestamp64Nano(?), ?)"
		args = append(args, position.Created, position.ID)
	}
	query += " ORDER BY CreatedAt DESC, Id DESC LIMIT ?"
	args = append(args, opts.Limit+pageLookahead)
	return fmt.Sprintf("%s SETTINGS max_execution_time = %d", query, opts.Timeout), args, position, nil
}

func QueryEvents(
	ctx context.Context,
	conn driver.Conn,
	req *observabilityv1.QueryEventsRequest,
	opts EventQueryOptions,
) (*observabilityv1.QueryEventsResponse, error) {
	query, args, position, err := buildEventsQuery(req, opts)
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse workload event query: %w", err)
	}
	defer rows.Close()
	response := &observabilityv1.QueryEventsResponse{}
	for rows.Next() {
		event := &observabilityv1.WorkloadEvent{}
		var created, first, last time.Time
		var severity uint8
		if err := rows.Scan(
			&event.Id,
			&created,
			&event.ResourceId,
			&event.ResourceName,
			&event.EnvironmentId,
			&event.Region,
			&severity,
			&event.Code,
			&event.Summary,
			&event.Occurrences,
			&first,
			&last,
		); err != nil {
			return nil, fmt.Errorf("scan workload event: %w", err)
		}
		if int32(len(response.GetEvents())) == opts.Limit {
			cursor, encodeErr := position.encode()
			if encodeErr != nil {
				return nil, encodeErr
			}
			response.NextCursor = cursor
			break
		}
		event.Severity = observabilityv1.WorkloadEventSeverity(severity)
		event.FirstSeen = timestamppb.New(first)
		event.LastSeen = timestamppb.New(last)
		response.Events = append(response.Events, event)
		position.Created = created.UnixNano()
		position.ID = event.GetId()
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workload event iteration: %w", err)
	}
	return response, nil
}
