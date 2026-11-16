package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	"github.com/team-loco/loco/internal/logquery"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	defaultEventPage   = 100
	streamBatch        = 500
	streamPollInterval = time.Second
	cursorTxidBytes    = 8
	cursorBytes        = 16
)

var ErrInvalidCursor = errors.New("invalid event stream cursor")

type streamPosition struct {
	txid uint64
	seq  int64
}

func (p streamPosition) encode() string {
	buf := make([]byte, 0, cursorBytes)
	buf = binary.BigEndian.AppendUint64(buf, p.txid)
	buf = binary.BigEndian.AppendUint64(buf, uint64(p.seq))
	return base64.RawURLEncoding.EncodeToString(buf)
}

func decodeStreamCursor(cursor string) (streamPosition, error) {
	if cursor == "" {
		return streamPosition{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) != cursorBytes {
		return streamPosition{}, ErrInvalidCursor
	}
	txid := binary.BigEndian.Uint64(raw[:cursorTxidBytes])
	seq := binary.BigEndian.Uint64(raw[cursorTxidBytes:])
	return streamPosition{txid: txid, seq: int64(seq)}, nil
}

type EventServer struct {
	db      genDb.DBTX
	queries genDb.Querier
	authz   *authz.Authorizer
	poll    time.Duration
}

func NewEventServer(db genDb.DBTX, queries genDb.Querier) *EventServer {
	return &EventServer{db: db, queries: queries, poll: streamPollInterval, authz: authz.New(queries)}
}

func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func eventToProto(e genDb.Event) *eventv1.Event {
	out := &eventv1.Event{
		Seq:           e.Seq,
		Id:            e.ID.String(),
		Type:          e.Type,
		OrgId:         uuidString(e.OrgID),
		WorkspaceId:   uuidString(e.WorkspaceID),
		ResourceId:    uuidString(e.ResourceID),
		EnvironmentId: uuidString(e.EnvironmentID),
		ActorType:     e.ActorType,
		ActorId:       uuidString(e.ActorID),
		SubjectType:   derefString(e.SubjectType),
		SubjectId:     uuidString(e.SubjectID),
		RequestId:     derefString(e.RequestID),
		CreatedAt:     timestamppb.New(e.CreatedAt),
	}
	var data map[string]any
	if err := json.Unmarshal(e.Data, &data); err == nil {
		if st, stErr := structpb.NewStruct(data); stErr == nil {
			out.Data = st
		}
	}
	return out
}

func (s *EventServer) ListEvents(
	ctx context.Context,
	req *eventv1.ListEventsRequest,
) (*eventv1.ListEventsResponse, error) {
	if req.GetStartTime() != nil && req.GetEndTime() != nil &&
		!req.GetStartTime().AsTime().Before(req.GetEndTime().AsTime()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "start_time must be before end_time")
	}
	filter, err := logquery.Parse(req.GetQuery())
	if err == nil {
		err = events.ValidateQuery(filter)
	}
	if err != nil {
		return nil, querySyntaxError(err)
	}
	scope, err := s.authorizeEventScope(ctx, req)
	if err != nil {
		return nil, err
	}
	pageSize := req.GetPageSize()
	if pageSize == 0 {
		pageSize = defaultEventPage
	}
	rows, err := s.listAuditEvents(ctx, req, scope, filter, pageSize+1)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	response := &eventv1.ListEventsResponse{Events: rows}
	if len(rows) > int(pageSize) {
		response.Events = rows[:pageSize]
		response.NextBeforeSeq = response.GetEvents()[len(response.GetEvents())-1].GetSeq()
	}
	return response, nil
}

type eventScopeKind int

const (
	eventScopeOrg eventScopeKind = iota
	eventScopeWorkspace
	eventScopeResource
)

// eventScope is the narrowest level a ListEvents request reads. workspaceID is set for the
// workspace and resource levels, resourceID for the resource level.
type eventScope struct {
	kind        eventScopeKind
	orgID       uuid.UUID
	workspaceID uuid.UUID
	resourceID  uuid.UUID
}

func (s *EventServer) authorizeEventScope(ctx context.Context, req *eventv1.ListEventsRequest) (eventScope, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return eventScope{}, err
	}
	if req.GetResourceId() != "" {
		resourceID, err := parseID("resource_id", req.GetResourceId())
		if err != nil {
			return eventScope{}, err
		}
		readErr := authorize(ctx, s.authz, actions.ListResourceAuditEvents, resourceID)
		if readErr != nil {
			if adminErr := authorize(ctx, s.authz, actions.ListResourceAuditAdminEvents, resourceID); adminErr != nil {
				return eventScope{}, readErr
			}
		}
		parents, err := s.queries.GetWorkspaceOrganizationIDByResourceID(ctx, resourceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return eventScope{}, rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
		}
		if err != nil {
			return eventScope{}, fmt.Errorf("get the parents of resource %s: %w", resourceID, err)
		}
		if parents.OrgID != orgID ||
			(req.GetWorkspaceId() != "" && parents.WorkspaceID.String() != req.GetWorkspaceId()) {
			return eventScope{}, rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
		}
		return eventScope{
			kind:        eventScopeResource,
			orgID:       orgID,
			workspaceID: parents.WorkspaceID,
			resourceID:  resourceID,
		}, nil
	}
	if req.GetWorkspaceId() != "" {
		workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
		if err != nil {
			return eventScope{}, err
		}
		if err = authorize(ctx, s.authz, actions.ListWorkspaceAuditEvents, workspaceID); err != nil {
			return eventScope{}, err
		}
		parent, err := s.queries.GetOrganizationIDByWorkspaceID(ctx, workspaceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return eventScope{}, rpcerr.New(connect.CodeNotFound, ErrWorkspaceNotFound)
		}
		if err != nil {
			return eventScope{}, fmt.Errorf("get the organization of workspace %s: %w", workspaceID, err)
		}
		if parent != orgID {
			return eventScope{}, rpcerr.New(connect.CodeNotFound, ErrWorkspaceNotFound)
		}
		return eventScope{kind: eventScopeWorkspace, orgID: orgID, workspaceID: workspaceID}, nil
	}
	if err := authorize(ctx, s.authz, actions.ListOrgAuditEvents, orgID); err != nil {
		return eventScope{}, err
	}
	return eventScope{kind: eventScopeOrg, orgID: orgID}, nil
}

func (s *EventServer) listAuditEvents(
	ctx context.Context,
	req *eventv1.ListEventsRequest,
	scope eventScope,
	filter logquery.Expr,
	maxRows int32,
) ([]*eventv1.Event, error) {
	query := events.AuditQuery{
		OrgID:     scope.orgID,
		BeforeSeq: req.GetBeforeSeq(),
		Filter:    filter,
		Limit:     maxRows,
	}
	if scope.kind != eventScopeOrg {
		query.WorkspaceID = &scope.workspaceID
	}
	if scope.kind == eventScopeResource {
		query.ResourceID = &scope.resourceID
	}
	if req.GetEnvironmentId() != "" {
		id, err := parseID("environment_id", req.GetEnvironmentId())
		if err != nil {
			return nil, err
		}
		query.EnvironmentID = &id
	}
	if req.GetStartTime() != nil {
		query.Start = new(req.GetStartTime().AsTime())
	}
	if req.GetEndTime() != nil {
		query.End = new(req.GetEndTime().AsTime())
	}
	rows, err := events.ListAudit(ctx, s.db, query)
	if err != nil {
		return nil, err
	}
	out := make([]*eventv1.Event, len(rows))
	for i, row := range rows {
		out[i] = eventToProto(row.Event)
		out[i].ActorEmail = derefString(row.ActorEmail)
		out[i].ActorName = derefString(row.ActorName)
	}
	return out, nil
}

func (s *EventServer) StreamEvents(
	ctx context.Context,
	req *eventv1.StreamEventsRequest,
	stream eventv1connect.EventServiceStreamEventsServerStream,
) error {
	if err := authorizeSystem(ctx, s.authz, actions.StreamEvents); err != nil {
		return err
	}
	position, err := decodeStreamCursor(req.GetCursor())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	for {
		rows, err := s.queries.ListEventsAfter(ctx, genDb.ListEventsAfterParams{
			AfterTxid: position.txid,
			AfterSeq:  position.seq,
			MaxRows:   streamBatch,
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read events for stream: %w", err)
		}
		if len(rows) > 0 {
			batch := make([]*eventv1.Event, len(rows))
			for i, row := range rows {
				batch[i] = eventToProto(row)
			}
			last := rows[len(rows)-1]
			position = streamPosition{txid: last.Txid, seq: last.Seq}
			resp := &eventv1.StreamEventsResponse{Events: batch, Cursor: position.encode()}
			if sendErr := stream.Send(resp); sendErr != nil {
				return sendErr
			}
			if len(rows) == streamBatch {
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
