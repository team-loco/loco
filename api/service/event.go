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

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	"github.com/team-loco/loco/internal/logquery"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	defaultEventPage   = 100
	streamBatch        = 500
	streamPollInterval = time.Second
	cursorBytes        = 8
)

var ErrInvalidCursor = errors.New("invalid event stream cursor")

type streamPosition int64

func (p streamPosition) encode() string {
	buf := make([]byte, 0, cursorBytes)
	buf = binary.BigEndian.AppendUint64(buf, uint64(p))
	return base64.RawURLEncoding.EncodeToString(buf)
}

func decodeStreamCursor(cursor string) (streamPosition, error) {
	if cursor == "" {
		return streamPosition(0), nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) != cursorBytes {
		return streamPosition(0), ErrInvalidCursor
	}
	return streamPosition(binary.BigEndian.Uint64(raw)), nil
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
	filter, err := parseEventFilter(req)
	if err != nil {
		return nil, err
	}
	scope, err := s.authorizeEventScope(ctx, req, actions.ListWorkspaceAuditEvents)
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

type eventScope struct {
	kind        eventScopeKind
	orgID       uuid.UUID
	workspaceID uuid.UUID
	resourceID  uuid.UUID
}

func (s *EventServer) authorizeEventScope(
	ctx context.Context,
	req *eventv1.ListEventsRequest,
	workspaceAction actions.Action,
) (eventScope, error) {
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
		if err = authorize(ctx, s.authz, workspaceAction, workspaceID); err != nil {
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
	query, err := auditQuery(req, scope, filter, maxRows)
	if err != nil {
		return nil, err
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
) (err error) {
	var scoped *eventv1.ListEventsRequest
	var filter logquery.Expr
	if req.GetWorkspaceId() != "" {
		scoped = &eventv1.ListEventsRequest{
			OrgId:         req.GetOrgId(),
			WorkspaceId:   req.GetWorkspaceId(),
			ResourceId:    req.GetResourceId(),
			EnvironmentId: req.GetEnvironmentId(),
			StartTime:     req.GetStartTime(),
			EndTime:       req.GetEndTime(),
			Query:         req.GetQuery(),
		}
		filter, err = parseEventFilter(scoped)
		if err != nil {
			return err
		}
	} else {
		if req.GetOrgId() != "" || req.GetResourceId() != "" || req.GetEnvironmentId() != "" || req.GetQuery() != "" ||
			req.GetStartTime() != nil ||
			req.GetEndTime() != nil {
			return connect.NewError(connect.CodeInvalidArgument, "workspace_id is required for event filters")
		}
		if authorizationErr := authorizeSystem(ctx, s.authz, actions.StreamEvents); authorizationErr != nil {
			return authorizationErr
		}
	}
	ctx, stop, err := auth.GuardStream(ctx, func(check context.Context) error {
		if scoped == nil {
			return authorizeSystem(check, s.authz, actions.StreamEvents)
		}
		_, checkErr := s.authorizeStreamScope(check, scoped)
		return checkErr
	})
	if err != nil {
		return err
	}
	defer func() {
		if authErr := stop(); authErr != nil {
			err = authErr
		}
	}()
	position, err := decodeStreamCursor(req.GetCursor())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	for {
		var rows []genDb.Event
		if scoped == nil {
			rows, err = s.queries.ListEventsAfter(
				ctx,
				genDb.ListEventsAfterParams{AfterPosition: int64(position), MaxRows: streamBatch},
			)
		} else {
			var scope eventScope
			scope, err = s.authorizeStreamScope(ctx, scoped)
			if err != nil {
				return err
			}
			query, queryErr := auditQuery(scoped, scope, filter, streamBatch)
			if queryErr != nil {
				return queryErr
			}
			query.Stream, query.AfterPosition = true, int64(position)
			var audit []events.AuditEvent
			audit, err = events.ListAudit(ctx, s.db, query)
			for _, row := range audit {
				rows = append(rows, row.Event)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read events for stream: %w", err)
		}
		if len(rows) > 0 {
			batch := make([]*eventv1.Event, len(rows))
			for i, row := range rows {
				if scoped != nil {
					if _, authorizationErr := s.authorizeStreamScope(ctx, scoped); authorizationErr != nil {
						return authorizationErr
					}
				}
				batch[i] = eventToProto(row)
			}
			last := rows[len(rows)-1]
			position = streamPosition(*last.StreamPosition)
			resp := &eventv1.StreamEventsResponse{Events: batch, Cursor: position.encode()}
			if err = ctx.Err(); err != nil {
				return err
			}
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

func (s *EventServer) authorizeStreamScope(ctx context.Context, req *eventv1.ListEventsRequest) (eventScope, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return eventScope{}, err
	}
	if p.ActorType == genDb.DeploymentActorTypeUser {
		p.Scopes, err = s.authz.UserScopes(ctx, p.UserID)
		if err != nil {
			return eventScope{}, fmt.Errorf("read stream permissions: %w", err)
		}
	}
	ctx = reqctx.WithPrincipal(ctx, p)
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return eventScope{}, err
	}
	if err := authorize(ctx, s.authz, actions.StreamWorkspaceEvents, workspaceID); err != nil {
		return eventScope{}, err
	}
	return s.authorizeEventScope(ctx, req, actions.StreamWorkspaceEvents)
}

func parseEventFilter(req *eventv1.ListEventsRequest) (logquery.Expr, error) {
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
	return filter, nil
}

func auditQuery(
	req *eventv1.ListEventsRequest,
	scope eventScope,
	filter logquery.Expr,
	maxRows int32,
) (events.AuditQuery, error) {
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
			return events.AuditQuery{}, err
		}
		query.EnvironmentID = &id
	}
	if req.GetStartTime() != nil {
		query.Start = new(req.GetStartTime().AsTime())
	}
	if req.GetEndTime() != nil {
		query.End = new(req.GetEndTime().AsTime())
	}
	return query, nil
}
