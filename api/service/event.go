package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
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
	queries genDb.Querier
	authz   *authz.Authorizer
	machine *tvm.VendingMachine
	poll    time.Duration
}

func NewEventServer(queries genDb.Querier, machine *tvm.VendingMachine) *EventServer {
	return &EventServer{queries: queries, machine: machine, poll: streamPollInterval, authz: authz.New(nil, queries)}
}

func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func eventToProto(e genDb.Event) *eventv1.Event {
	out := &eventv1.Event{
		Seq:         e.Seq,
		Id:          e.ID.String(),
		Type:        e.Type,
		OrgId:       uuidString(e.OrgID),
		WorkspaceId: uuidString(e.WorkspaceID),
		ResourceId:  uuidString(e.ResourceID),
		ActorType:   e.ActorType,
		ActorId:     uuidString(e.ActorID),
		SubjectType: derefString(e.SubjectType),
		SubjectId:   uuidString(e.SubjectID),
		RequestId:   derefString(e.RequestID),
		CreatedAt:   timestamppb.New(e.CreatedAt),
	}
	var data map[string]any
	if err := json.Unmarshal(e.Data, &data); err == nil {
		if st, stErr := structpb.NewStruct(data); stErr == nil {
			out.Data = st
		}
	}
	return out
}

func (s *EventServer) verify(ctx context.Context, scope genDb.EntityScope) error {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		return rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	if err := s.authz.Check(ctx, scopes, scope); err != nil {
		return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	return nil
}

func (s *EventServer) ListEvents(
	ctx context.Context,
	req *eventv1.ListEventsRequest,
) (*eventv1.ListEventsResponse, error) {
	workspaceID, err := s.authorizeEventScope(ctx, req)
	if err != nil {
		return nil, err
	}
	pageSize := req.GetPageSize()
	if pageSize == 0 {
		pageSize = defaultEventPage
	}
	rows, err := s.listAuditEvents(ctx, req, workspaceID, pageSize+1)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list audit events", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	response := &eventv1.ListEventsResponse{Events: rows}
	if len(rows) > int(pageSize) {
		response.Events = rows[:pageSize]
		response.NextBeforeSeq = response.GetEvents()[len(response.GetEvents())-1].GetSeq()
	}
	return response, nil
}

func (s *EventServer) authorizeEventScope(ctx context.Context, req *eventv1.ListEventsRequest) (*uuid.UUID, error) {
	orgID := uuid.MustParse(req.GetOrgId())
	if req.GetResourceId() != "" {
		readErr := s.verify(ctx, actions.New(actions.ListResourceAuditEvents, req.GetResourceId()))
		if readErr != nil {
			if adminErr := s.verify(
				ctx,
				actions.New(actions.ListResourceAuditAdminEvents, req.GetResourceId()),
			); adminErr != nil {
				return nil, readErr
			}
		}
		resourceID := uuid.MustParse(req.GetResourceId())
		parents, err := s.queries.GetWorkspaceOrganizationIDByResourceID(ctx, resourceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
		}
		if err != nil {
			return nil, rpcerr.New(connect.CodeInternal, ErrDB)
		}
		if parents.OrgID != orgID ||
			(req.GetWorkspaceId() != "" && parents.WorkspaceID.String() != req.GetWorkspaceId()) {
			return nil, rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
		}
		return new(parents.WorkspaceID), nil
	}
	if req.GetWorkspaceId() != "" {
		if err := s.verify(ctx, actions.New(actions.ListWorkspaceAuditEvents, req.GetWorkspaceId())); err != nil {
			return nil, err
		}
		workspaceID := uuid.MustParse(req.GetWorkspaceId())
		parent, err := s.queries.GetOrganizationIDByWorkspaceID(ctx, workspaceID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, rpcerr.New(connect.CodeNotFound, ErrWorkspaceNotFound)
		}
		if err != nil {
			return nil, rpcerr.New(connect.CodeInternal, ErrDB)
		}
		if parent != orgID {
			return nil, rpcerr.New(connect.CodeNotFound, ErrWorkspaceNotFound)
		}
		return &workspaceID, nil
	}
	if err := s.verify(ctx, actions.New(actions.ListOrgAuditEvents, req.GetOrgId())); err != nil {
		return nil, err
	}
	return nil, nil
}

func auditEvent(row genDb.Event, email, name *string) *eventv1.Event {
	event := eventToProto(row)
	event.ActorEmail = derefString(email)
	event.ActorName = derefString(name)
	return event
}

func (s *EventServer) listAuditEvents(
	ctx context.Context,
	req *eventv1.ListEventsRequest,
	workspaceID *uuid.UUID,
	maxRows int32,
) ([]*eventv1.Event, error) {
	orgID := uuid.MustParse(req.GetOrgId())
	types := req.GetTypes()
	if types == nil {
		types = []string{}
	}
	var before *int64
	if req.GetBeforeSeq() > 0 {
		before = new(req.GetBeforeSeq())
	}
	out := []*eventv1.Event{}
	if req.GetResourceId() != "" {
		resourceID := uuid.MustParse(req.GetResourceId())
		rows, err := s.queries.ListResourceAuditEvents(ctx, genDb.ListResourceAuditEventsParams{
			OrgID:       &orgID,
			WorkspaceID: workspaceID,
			ResourceID:  &resourceID,
			Types:       types,
			BeforeSeq:   before,
			MaxRows:     maxRows,
		})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, auditEvent(row.Event, row.ActorEmail, row.ActorName))
		}
		return out, nil
	}
	if workspaceID != nil {
		rows, err := s.queries.ListWorkspaceAuditEvents(ctx, genDb.ListWorkspaceAuditEventsParams{
			OrgID: &orgID, WorkspaceID: workspaceID, Types: types, BeforeSeq: before, MaxRows: maxRows})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, auditEvent(row.Event, row.ActorEmail, row.ActorName))
		}
		return out, nil
	}
	rows, err := s.queries.ListOrgAuditEvents(
		ctx,
		genDb.ListOrgAuditEventsParams{OrgID: &orgID, Types: types, BeforeSeq: before, MaxRows: maxRows},
	)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out = append(out, auditEvent(row.Event, row.ActorEmail, row.ActorName))
	}
	return out, nil
}

func (s *EventServer) StreamEvents(
	ctx context.Context,
	req *eventv1.StreamEventsRequest,
	stream eventv1connect.EventServiceStreamEventsServerStream,
) error {
	if err := s.verify(ctx, actions.NewSystem(actions.StreamEvents)); err != nil {
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
			slog.ErrorContext(ctx, "failed to read events for stream", "error", err)
			return rpcerr.New(connect.CodeInternal, ErrDB)
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
