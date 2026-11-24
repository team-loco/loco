package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/tracing"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	"github.com/team-loco/loco/internal/logquery"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	defaultEventPage    = 100
	streamBatch         = 500
	streamPollInterval  = time.Second
	eventStreamPollSpan = "events.stream.poll"
	cursorBytes         = 8
)

var ErrInvalidCursor = errors.New("invalid event cursor")

// eventPosition is an event's stream position or seq, as the opaque token clients send back.
type eventPosition int64

func (p eventPosition) encode() string {
	buf := make([]byte, 0, cursorBytes)
	buf = binary.BigEndian.AppendUint64(buf, uint64(p))
	return base64.RawURLEncoding.EncodeToString(buf)
}

func decodeEventPosition(cursor string) (eventPosition, error) {
	if cursor == "" {
		return eventPosition(0), nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) != cursorBytes {
		return eventPosition(0), ErrInvalidCursor
	}
	return eventPosition(binary.BigEndian.Uint64(raw)), nil
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
		Seq:            e.Seq,
		Id:             e.ID.String(),
		Type:           e.Type,
		OrgId:          uuidString(e.OrgID),
		WorkspaceId:    uuidString(e.WorkspaceID),
		ResourceId:     uuidString(e.ResourceID),
		EnvironmentId:  uuidString(e.EnvironmentID),
		DeploymentId:   uuidString(e.DeploymentID),
		ActorType:      e.ActorType,
		ActorId:        uuidString(e.ActorID),
		ActorTokenType: derefString(e.ActorTokenType),
		SubjectType:    derefString(e.SubjectType),
		SubjectId:      uuidString(e.SubjectID),
		RequestId:      derefString(e.RequestID),
		CreatedAt:      timestamppb.New(e.CreatedAt),
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
	before, err := decodeEventPosition(req.GetPageToken())
	if err != nil {
		return nil, fieldError(connect.CodeInvalidArgument, "page_token", publicInvalidPageToken, "")
	}
	pageSize := req.GetPageSize()
	if pageSize == 0 {
		pageSize = defaultEventPage
	}
	query, err := auditQuery(req, scope, filter)
	if err != nil {
		return nil, err
	}
	total, err := totalCount(ctx, req.GetIncludeTotalCount(), func(ctx context.Context, limit int32) (int64, error) {
		counted := query
		counted.Limit = limit
		return events.CountAudit(ctx, s.db, counted)
	})
	if err != nil {
		return nil, fmt.Errorf("count audit events: %w", err)
	}
	query.BeforeSeq = int64(before)
	query.Limit = pageSize + 1
	rows, err := s.listAuditEvents(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	response := &eventv1.ListEventsResponse{Events: rows, TotalCount: total}
	if req.GetIncludeFacets() {
		faceted := query
		faceted.BeforeSeq, faceted.Limit = 0, totalCountCap+1
		if response.Facets, err = s.facets(ctx, faceted); err != nil {
			return nil, err
		}
	}
	if len(rows) > int(pageSize) {
		response.Events = rows[:pageSize]
		response.NextPageToken = eventPosition(response.GetEvents()[len(response.GetEvents())-1].GetSeq()).encode()
	}
	return response, nil
}

func (s *EventServer) facets(ctx context.Context, query events.AuditQuery) ([]*eventv1.EventFacet, error) {
	rows, err := events.FacetAudit(ctx, s.db, query)
	if err != nil {
		return nil, fmt.Errorf("count event facets: %w", err)
	}
	var out []*eventv1.EventFacet
	for _, row := range rows {
		if len(out) == 0 || out[len(out)-1].GetKey() != row.Key {
			out = append(out, &eventv1.EventFacet{Key: row.Key})
		}
		facet := out[len(out)-1]
		facet.Values = append(facet.Values, &eventv1.EventFacetValue{Value: row.Value, Count: cappedCount(row.Count)})
	}
	return out, nil
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
	// resourceIDs, when set, narrows a workspace scope to the resources the caller can list.
	resourceIDs []uuid.UUID
}

// readableResources are the workspace's resources whose events the caller can list, by the checks a
// ListEvents for one resource makes. A caller without a scope on the workspace or its organization
// reads a resource only through a grant on it, so only those resources are checked.
func (s *EventServer) readableResources(
	ctx context.Context,
	orgID uuid.UUID,
	workspaceID uuid.UUID,
) ([]uuid.UUID, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	allWorkspaces, workspaceIDs := s.authz.ReadableWorkspaces(ctx, p.Scopes, orgID)
	granted := grantedResourceIDs(p.Scopes)
	scoped := allWorkspaces || slices.Contains(workspaceIDs, workspaceID)
	if !scoped && len(granted) == 0 {
		return nil, nil
	}
	rows, err := s.queries.ListWorkspaceResourceSlugs(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list the resources of workspace %s: %w", workspaceID, err)
	}
	var readable []uuid.UUID
	for _, row := range rows {
		if !scoped && !slices.Contains(granted, row.ID) {
			continue
		}
		if authorize(ctx, s.authz, actions.ListResourceAuditEvents, row.ID) == nil ||
			authorize(ctx, s.authz, actions.ListResourceAuditAdminEvents, row.ID) == nil {
			readable = append(readable, row.ID)
		}
	}
	return readable, nil
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
		if len(req.GetResourceIds()) > 0 {
			return eventScope{}, connect.NewError(
				connect.CodeInvalidArgument,
				"set resource_id or resource_ids, not both",
			)
		}
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
		requested, err := parseIDs("resource_ids", req.GetResourceIds())
		if err != nil {
			return eventScope{}, err
		}
		denied := authorize(ctx, s.authz, workspaceAction, workspaceID)
		resources := requested
		if denied != nil {
			if !req.GetReadableResources() {
				return eventScope{}, denied
			}
			readable, readErr := s.readableResources(ctx, orgID, workspaceID)
			if readErr != nil {
				return eventScope{}, readErr
			}
			if len(readable) == 0 {
				return eventScope{}, denied
			}
			resources = readable
			if requested != nil {
				resources = slices.DeleteFunc(readable, func(id uuid.UUID) bool {
					return !slices.Contains(requested, id)
				})
			}
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
		return eventScope{
			kind: eventScopeWorkspace, orgID: orgID, workspaceID: workspaceID, resourceIDs: resources,
		}, nil
	}
	if err := authorize(ctx, s.authz, actions.ListOrgAuditEvents, orgID); err != nil {
		return eventScope{}, err
	}
	return eventScope{kind: eventScopeOrg, orgID: orgID}, nil
}

func (s *EventServer) listAuditEvents(ctx context.Context, query events.AuditQuery) ([]*eventv1.Event, error) {
	rows, err := events.ListAudit(ctx, s.db, query)
	if err != nil {
		return nil, err
	}
	out := make([]*eventv1.Event, len(rows))
	for i, row := range rows {
		out[i] = eventToProto(row.Event)
		out[i].ActorEmail = derefString(row.ActorEmail)
		out[i].ActorName = derefString(row.ActorName)
		out[i].Title, out[i].Severity, out[i].Region = row.Title, row.Severity, row.Region
	}
	return out, nil
}

func (s *EventServer) StreamEvents(
	ctx context.Context,
	req *eventv1.StreamEventsRequest,
	stream eventv1connect.EventServiceStreamEventsServerStream,
) (err error) {
	var scoped *eventv1.ListEventsRequest
	var scope eventScope
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
		if scope, err = s.authorizeStreamScope(ctx, scoped); err != nil {
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
	position, err := decodeEventPosition(req.GetCursor())
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	// The stream sends only when events arrive; open it for the client before the first one.
	if err = stream.SendHeaders(); err != nil {
		return err
	}
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	for {
		var rows []events.AuditEvent
		pollCtx, span := tracing.StartLinked(ctx, eventStreamPollSpan)
		if scoped == nil {
			var all []genDb.Event
			all, err = s.queries.ListEventsAfter(
				pollCtx,
				genDb.ListEventsAfterParams{AfterPosition: int64(position), MaxRows: streamBatch},
			)
			for _, row := range all {
				rows = append(rows, events.AuditEvent{Event: row})
			}
		} else {
			query, queryErr := auditQuery(scoped, scope, filter)
			if queryErr != nil {
				tracing.End(span, queryErr)
				return queryErr
			}
			query.Stream, query.AfterPosition, query.Limit = true, int64(position), streamBatch
			rows, err = events.ListAudit(pollCtx, s.db, query)
		}
		tracing.EndLive(ctx, span, err)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read events for stream: %w", err)
		}
		if len(rows) > 0 {
			batch := make([]*eventv1.Event, len(rows))
			for i, row := range rows {
				batch[i] = eventToProto(row.Event)
				batch[i].Title, batch[i].Severity, batch[i].Region = row.Title, row.Severity, row.Region
			}
			last := rows[len(rows)-1]
			position = eventPosition(*last.StreamPosition)
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
		public := connect.NewError(connect.CodeInvalidArgument, "end_time must be after start_time")
		return nil, withDetail(public, &errdetails.BadRequest{
			FieldViolations: []*errdetails.BadRequest_FieldViolation{
				{Field: "end_time", Description: "must be after start_time"},
			},
		})
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

func auditQuery(req *eventv1.ListEventsRequest, scope eventScope, filter logquery.Expr) (events.AuditQuery, error) {
	query := events.AuditQuery{OrgID: scope.orgID, Filter: filter, Types: req.GetTypes()}
	if scope.kind != eventScopeOrg {
		query.WorkspaceID = &scope.workspaceID
	}
	if scope.kind == eventScopeResource {
		query.ResourceID = &scope.resourceID
	}
	query.ResourceIDs = scope.resourceIDs
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
