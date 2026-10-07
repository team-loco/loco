package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultEventPage   = 100
	streamBatch        = 500
	streamPollInterval = time.Second
)

type EventServer struct {
	queries genDb.Querier
	machine *tvm.VendingMachine
	poll    time.Duration
}

func NewEventServer(queries genDb.Querier, machine *tvm.VendingMachine) *EventServer {
	return &EventServer{queries: queries, machine: machine, poll: streamPollInterval}
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
		return connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, scope); err != nil {
		return connect.NewError(connect.CodePermissionDenied, err)
	}
	return nil
}

func (s *EventServer) ListOrgEvents(
	ctx context.Context,
	req *connect.Request[eventv1.ListOrgEventsRequest],
) (*connect.Response[eventv1.ListOrgEventsResponse], error) {
	r := req.Msg
	if err := s.verify(ctx, actions.New(actions.ListOrgEvents, r.GetOrgId())); err != nil {
		return nil, err
	}
	pageSize := r.GetPageSize()
	if pageSize == 0 {
		pageSize = defaultEventPage
	}
	params := genDb.ListOrgEventsParams{
		OrgID:   new(uuid.MustParse(r.GetOrgId())),
		Types:   r.GetTypes(),
		MaxRows: pageSize,
	}
	if params.Types == nil {
		params.Types = []string{}
	}
	if r.GetBeforeSeq() > 0 {
		before := r.GetBeforeSeq()
		params.BeforeSeq = &before
	}
	rows, err := s.queries.ListOrgEvents(ctx, params)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list org events", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	out := make([]*eventv1.Event, len(rows))
	for i, row := range rows {
		out[i] = eventToProto(row)
	}
	resp := &eventv1.ListOrgEventsResponse{Events: out}
	if len(rows) == int(pageSize) {
		resp.NextBeforeSeq = rows[len(rows)-1].Seq
	}
	return connect.NewResponse(resp), nil
}

func (s *EventServer) StreamEvents(
	ctx context.Context,
	req *connect.Request[eventv1.StreamEventsRequest],
	stream *connect.ServerStream[eventv1.StreamEventsResponse],
) error {
	if err := s.verify(ctx, actions.NewSystem(actions.StreamEvents)); err != nil {
		return err
	}
	cursor := req.Msg.GetAfterSeq()
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	for {
		rows, err := s.queries.ListEventsAfter(ctx, genDb.ListEventsAfterParams{Seq: cursor, Limit: streamBatch})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.ErrorContext(ctx, "failed to read events for stream", "error", err)
			return connect.NewError(connect.CodeInternal, ErrDB)
		}
		if len(rows) > 0 {
			batch := make([]*eventv1.Event, len(rows))
			for i, row := range rows {
				batch[i] = eventToProto(row)
			}
			if sendErr := stream.Send(&eventv1.StreamEventsResponse{Events: batch}); sendErr != nil {
				return sendErr
			}
			cursor = rows[len(rows)-1].Seq
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
