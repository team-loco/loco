package service

import (
	"context"
	"errors"
	"net/url"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/webhooks"
	webhookv1 "github.com/team-loco/loco/gen/go/loco/webhook/v1"
	"github.com/team-loco/loco/internal/rpcerr"
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
		return genDb.Entity{}, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		return genDb.Entity{}, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	if err := s.authz.Check(ctx, scopes, actions.New(actions.ManageWorkspaceWebhooks, workspaceID)); err != nil {
		return genDb.Entity{}, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
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
	req *webhookv1.CreateWebhookRequest,
) (*webhookv1.CreateWebhookResponse, error) {
	entity, err := s.requireWorkspaceAdmin(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if !s.validURL(req.GetUrl()) {
		return nil, rpcerr.New(connect.CodeInvalidArgument, ErrWebhookURL)
	}
	workspaceID := uuid.MustParse(req.GetWorkspaceId())
	secret, err := webhooks.NewSecret()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, publicRequestFailed).WithCause(err)
	}
	var createdBy *uuid.UUID
	if entity.Type == genDb.EntityTypeUser {
		createdBy = new(entity.ID)
	}
	types := req.GetEventTypes()
	if types == nil {
		types = []string{}
	}
	var row genDb.Webhook
	err = pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		qtx := genDb.New(tx)
		_, lockErr := qtx.LockWorkspaceForWebhookCreation(ctx, workspaceID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return rpcerr.New(connect.CodeNotFound, ErrWorkspaceNotFound)
		}
		if lockErr != nil {
			return lockErr
		}
		count, countErr := qtx.CountWorkspaceWebhooks(ctx, workspaceID)
		if countErr != nil {
			return countErr
		}
		if count >= maxWebhooksPerWorkspace {
			return rpcerr.New(connect.CodeFailedPrecondition, ErrWebhookLimit)
		}
		created, createErr := qtx.CreateWorkspaceWebhook(ctx, genDb.CreateWorkspaceWebhookParams{
			WorkspaceID: workspaceID,
			Url:         req.GetUrl(),
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
		return nil, txError(ctx, "failed to create webhook", err)
	}
	return &webhookv1.CreateWebhookResponse{Webhook: webhookToProto(row), Secret: secret}, nil
}

func (s *WebhookServer) ListWebhooks(
	ctx context.Context,
	req *webhookv1.ListWebhooksRequest,
) (*webhookv1.ListWebhooksResponse, error) {
	if _, err := s.requireWorkspaceAdmin(ctx, req.GetWorkspaceId()); err != nil {
		return nil, err
	}
	workspaceID := uuid.MustParse(req.GetWorkspaceId())
	rows, err := s.queries.ListWorkspaceWebhooks(ctx, workspaceID)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	out := make([]*webhookv1.Webhook, len(rows))
	for i, row := range rows {
		out[i] = webhookToProto(row)
	}
	return &webhookv1.ListWebhooksResponse{Webhooks: out}, nil
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
		return row, rpcerr.New(connect.CodeNotFound, ErrWebhookNotFound)
	}
	if err != nil {
		return row, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	return row, nil
}

func (s *WebhookServer) DeleteWebhook(
	ctx context.Context,
	req *webhookv1.DeleteWebhookRequest,
) (*webhookv1.DeleteWebhookResponse, error) {
	if _, err := s.requireWorkspaceAdmin(ctx, req.GetWorkspaceId()); err != nil {
		return nil, err
	}
	workspaceID := uuid.MustParse(req.GetWorkspaceId())
	row, err := s.loadWebhook(ctx, workspaceID, req.GetWebhookId())
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
	return &webhookv1.DeleteWebhookResponse{}, nil
}

func (s *WebhookServer) ListWebhookDeliveries(
	ctx context.Context,
	req *webhookv1.ListWebhookDeliveriesRequest,
) (*webhookv1.ListWebhookDeliveriesResponse, error) {
	if _, err := s.requireWorkspaceAdmin(ctx, req.GetWorkspaceId()); err != nil {
		return nil, err
	}
	workspaceID := uuid.MustParse(req.GetWorkspaceId())
	row, err := s.loadWebhook(ctx, workspaceID, req.GetWebhookId())
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListWebhookDeliveries(ctx, genDb.ListWebhookDeliveriesParams{
		WebhookID: row.ID,
		MaxRows:   recentDeliveries,
	})
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
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
	return &webhookv1.ListWebhookDeliveriesResponse{Deliveries: out}, nil
}
