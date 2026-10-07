package service

import (
	"context"
	"errors"
	"log/slog"
	"net/url"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/webhooks"
	webhookv1 "github.com/team-loco/loco/gen/go/loco/webhook/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	maxWebhooksPerWorkspace = 10
	recentDeliveries        = 25
	schemeHTTPS             = "https"
	schemeHTTP              = "http"
	fieldWebhookURL         = "url"
	fieldWebhookEvents      = "eventTypes"
)

var (
	ErrWebhookURL      = errors.New("use an https URL with a host name")
	ErrWebhookLimit    = errors.New("a workspace can have at most 10 webhooks")
	ErrWebhookNotFound = errors.New("webhook not found in this workspace")
)

type WebhookServer struct {
	db           *pgxpool.Pool
	queries      genDb.Querier
	authz        *authz.Authorizer
	allowPrivate bool
}

func NewWebhookServer(db *pgxpool.Pool, queries genDb.Querier, allowPrivate bool) *WebhookServer {
	return &WebhookServer{db: db, queries: queries, authz: authz.New(db, queries), allowPrivate: allowPrivate}
}

func (s *WebhookServer) requireWorkspaceAdmin(ctx context.Context, workspaceID string) (genDb.Entity, error) {
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return genDb.Entity{}, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		return genDb.Entity{}, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	if err := s.authz.Check(ctx, scopes, actions.New(actions.ManageWorkspaceWebhooks, workspaceID)); err != nil {
		return genDb.Entity{}, connect.NewError(connect.CodePermissionDenied, err)
	}
	return entity, nil
}

func (s *WebhookServer) validURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return false
	}
	return u.Scheme == schemeHTTPS || (s.allowPrivate && u.Scheme == schemeHTTP)
}

func webhookToProto(w genDb.Webhook) *webhookv1.Webhook {
	return &webhookv1.Webhook{
		Id:         w.ID.String(),
		Url:        w.Url,
		EventTypes: w.EventTypes,
		CreatedAt:  timestamppb.New(w.CreatedAt),
	}
}

func (s *WebhookServer) CreateWebhook(
	ctx context.Context,
	req *connect.Request[webhookv1.CreateWebhookRequest],
) (*connect.Response[webhookv1.CreateWebhookResponse], error) {
	entity, err := s.requireWorkspaceAdmin(ctx, req.Msg.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if !s.validURL(req.Msg.GetUrl()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrWebhookURL)
	}
	workspaceID := uuid.MustParse(req.Msg.GetWorkspaceId())
	count, err := s.queries.CountWorkspaceWebhooks(ctx, workspaceID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if count >= maxWebhooksPerWorkspace {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrWebhookLimit)
	}
	secret, err := webhooks.NewSecret()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	var createdBy *uuid.UUID
	if entity.Type == genDb.EntityTypeUser {
		createdBy = new(entity.ID)
	}
	types := req.Msg.GetEventTypes()
	if types == nil {
		types = []string{}
	}
	var row genDb.Webhook
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		created, createErr := qtx.CreateWorkspaceWebhook(ctx, genDb.CreateWorkspaceWebhookParams{
			WorkspaceID: workspaceID,
			Url:         req.Msg.GetUrl(),
			Secret:      secret,
			EventTypes:  types,
			CreatedBy:   createdBy,
		})
		if createErr != nil {
			return createErr
		}
		row = created
		return events.Record(ctx, qtx, events.Event{
			Type:        events.WebhookCreated,
			WorkspaceID: new(workspaceID),
			SubjectType: events.SubjectWebhook,
			SubjectID:   new(row.ID),
			Data:        map[string]any{fieldWebhookURL: row.Url, fieldWebhookEvents: row.EventTypes},
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create webhook", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	return connect.NewResponse(&webhookv1.CreateWebhookResponse{Webhook: webhookToProto(row), Secret: secret}), nil
}

func (s *WebhookServer) ListWebhooks(
	ctx context.Context,
	req *connect.Request[webhookv1.ListWebhooksRequest],
) (*connect.Response[webhookv1.ListWebhooksResponse], error) {
	if _, err := s.requireWorkspaceAdmin(ctx, req.Msg.GetWorkspaceId()); err != nil {
		return nil, err
	}
	workspaceID := uuid.MustParse(req.Msg.GetWorkspaceId())
	rows, err := s.queries.ListWorkspaceWebhooks(ctx, workspaceID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	out := make([]*webhookv1.Webhook, len(rows))
	for i, row := range rows {
		out[i] = webhookToProto(row)
	}
	return connect.NewResponse(&webhookv1.ListWebhooksResponse{Webhooks: out}), nil
}

func (s *WebhookServer) loadWebhook(
	ctx context.Context,
	workspaceID uuid.UUID,
	webhookID string,
) (genDb.Webhook, error) {
	row, err := s.queries.GetWorkspaceWebhook(ctx, genDb.GetWorkspaceWebhookParams{
		ID:          uuid.MustParse(webhookID),
		WorkspaceID: workspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, connect.NewError(connect.CodeNotFound, ErrWebhookNotFound)
	}
	if err != nil {
		return row, connect.NewError(connect.CodeInternal, ErrDB)
	}
	return row, nil
}

func (s *WebhookServer) DeleteWebhook(
	ctx context.Context,
	req *connect.Request[webhookv1.DeleteWebhookRequest],
) (*connect.Response[webhookv1.DeleteWebhookResponse], error) {
	if _, err := s.requireWorkspaceAdmin(ctx, req.Msg.GetWorkspaceId()); err != nil {
		return nil, err
	}
	workspaceID := uuid.MustParse(req.Msg.GetWorkspaceId())
	row, err := s.loadWebhook(ctx, workspaceID, req.Msg.GetWebhookId())
	if err != nil {
		return nil, err
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, deleteErr := qtx.DeleteWorkspaceWebhook(
			ctx,
			genDb.DeleteWorkspaceWebhookParams{ID: row.ID, WorkspaceID: workspaceID},
		); deleteErr != nil {
			return deleteErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.WebhookDeleted,
			WorkspaceID: new(workspaceID),
			SubjectType: events.SubjectWebhook,
			SubjectID:   new(row.ID),
			Data:        map[string]any{fieldWebhookURL: row.Url},
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to delete webhook", err)
	}
	return connect.NewResponse(&webhookv1.DeleteWebhookResponse{}), nil
}

func (s *WebhookServer) ListWebhookDeliveries(
	ctx context.Context,
	req *connect.Request[webhookv1.ListWebhookDeliveriesRequest],
) (*connect.Response[webhookv1.ListWebhookDeliveriesResponse], error) {
	if _, err := s.requireWorkspaceAdmin(ctx, req.Msg.GetWorkspaceId()); err != nil {
		return nil, err
	}
	workspaceID := uuid.MustParse(req.Msg.GetWorkspaceId())
	row, err := s.loadWebhook(ctx, workspaceID, req.Msg.GetWebhookId())
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListWebhookDeliveries(ctx, genDb.ListWebhookDeliveriesParams{
		WebhookID: row.ID,
		MaxRows:   recentDeliveries,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	out := make([]*webhookv1.WebhookDelivery, len(rows))
	for i, d := range rows {
		delivery := &webhookv1.WebhookDelivery{
			Id:            d.ID.String(),
			EventId:       d.EventID.String(),
			EventType:     d.EventType,
			Status:        d.Status,
			Attempts:      d.Attempts,
			LastError:     derefString(d.LastError),
			CreatedAt:     timestamppb.New(d.CreatedAt),
			NextAttemptAt: timestamppb.New(d.NextAttemptAt),
		}
		if d.LastStatusCode != nil {
			delivery.LastStatusCode = *d.LastStatusCode
		}
		if d.DeliveredAt != nil {
			delivery.DeliveredAt = timestamppb.New(*d.DeliveredAt)
		}
		out[i] = delivery
	}
	return connect.NewResponse(&webhookv1.ListWebhookDeliveriesResponse{Deliveries: out}), nil
}
