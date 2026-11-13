package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/webhooksig"
	"github.com/team-loco/loco/api/webhooks"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	webhookv1 "github.com/team-loco/loco/gen/go/loco/webhook/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	fieldWebhookURL    = "url"
	fieldWebhookEvents = "eventTypes"
	fieldGracePeriod   = "gracePeriodSeconds"
)

var (
	ErrWebhookURL       = rpcerr.NewCoded(connect.CodeInvalidArgument, "use an https URL with a host name")
	ErrWebhookNotFound  = rpcerr.NewCoded(connect.CodeNotFound, "webhook not found in this workspace")
	ErrDeliveryNotFound = rpcerr.NewCoded(connect.CodeNotFound, "delivery not found for this webhook")
	ErrDeliveryPending  = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"the delivery is still pending; redeliver it once it succeeds or fails",
	)
)

type WebhookServer struct {
	db           *pgxpool.Pool
	queries      genDb.Querier
	authz        *authz.Authorizer
	provider     secretkeys.Provider
	allowPrivate bool
	counts       *configv1.CountLimits
}

func NewWebhookServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	provider secretkeys.Provider,
	allowPrivate bool,
	counts *configv1.CountLimits,
) *WebhookServer {
	return &WebhookServer{
		db:           db,
		queries:      queries,
		authz:        authz.New(db, queries),
		provider:     provider,
		allowPrivate: allowPrivate,
		counts:       counts,
	}
}

func (s *WebhookServer) validURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return false
	}
	return u.Scheme == schemeHTTPS || (s.allowPrivate && u.Scheme == schemeHTTP)
}

func webhookToProto(w genDb.Webhook) *webhookv1.Webhook {
	var previousExpires *timestamppb.Timestamp
	if w.PreviousSecretExpiresAt != nil && w.PreviousSecretExpiresAt.After(time.Now()) {
		previousExpires = timestamppb.New(*w.PreviousSecretExpiresAt)
	}
	return &webhookv1.Webhook{
		Id:                      w.ID.String(),
		Url:                     w.Url,
		EventTypes:              w.EventTypes,
		CreatedAt:               timestamppb.New(w.CreatedAt),
		PreviousSecretExpiresAt: previousExpires,
	}
}

func (s *WebhookServer) CreateWebhook(
	ctx context.Context,
	req *webhookv1.CreateWebhookRequest,
) (*webhookv1.CreateWebhookResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err = check(ctx, s.authz, p, actions.New(actions.ManageWorkspaceWebhooks, workspaceID)); err != nil {
		return nil, err
	}
	if !s.validURL(req.GetUrl()) {
		return nil, ErrWebhookURL
	}
	secret, err := webhooksig.NewSecret()
	if err != nil {
		return nil, fmt.Errorf("generate webhook secret: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate webhook id: %w", err)
	}
	key, sealed, err := webhooks.SealSecretWithNewKey(ctx, s.provider, id, secret)
	if err != nil {
		return nil, keyProviderUnavailable(err)
	}
	var createdBy *uuid.UUID
	if p.Entity.Type == genDb.EntityTypeUser {
		createdBy = new(p.Entity.ID)
	}
	types := req.GetEventTypes()
	if types == nil {
		types = []string{}
	}
	var row genDb.Webhook
	err = pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		qtx := genDb.New(tx)
		if limitErr := checkWorkspaceLimit(
			ctx, qtx, s.counts, limits.WebhooksPerWorkspace, workspaceID, qtx.CountWorkspaceWebhooks,
		); limitErr != nil {
			return limitErr
		}
		created, createErr := qtx.CreateWorkspaceWebhook(ctx, genDb.CreateWorkspaceWebhookParams{
			ID:          id,
			WorkspaceID: workspaceID,
			Url:         req.GetUrl(),
			Provider:    key.Provider,
			KekID:       key.KeyID,
			WrappedDek:  key.Bytes,
			Secret:      sealed,
			EventTypes:  types,
			CreatedBy:   createdBy,
		})
		if createErr != nil {
			return fmt.Errorf("insert webhook: %w", createErr)
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
		return nil, err
	}
	return &webhookv1.CreateWebhookResponse{Webhook: webhookToProto(row), Secret: secret}, nil
}

func (s *WebhookServer) ListWebhooks(
	ctx context.Context,
	req *webhookv1.ListWebhooksRequest,
) (*webhookv1.ListWebhooksResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ManageWorkspaceWebhooks, workspaceID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListWorkspaceWebhooks(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	out := make([]*webhookv1.Webhook, len(rows))
	for i, row := range rows {
		out[i] = webhookToProto(row)
	}
	return &webhookv1.ListWebhooksResponse{Webhooks: out}, nil
}

func (s *WebhookServer) UpdateWebhook(
	ctx context.Context,
	req *webhookv1.UpdateWebhookRequest,
) (*webhookv1.UpdateWebhookResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ManageWorkspaceWebhooks, workspaceID); err != nil {
		return nil, err
	}
	if !s.validURL(req.GetUrl()) {
		return nil, ErrWebhookURL
	}
	types := req.GetEventTypes()
	if types == nil {
		types = []string{}
	}
	var row genDb.Webhook
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		updated, updateErr := qtx.UpdateWorkspaceWebhook(ctx, genDb.UpdateWorkspaceWebhookParams{
			ID:          uuid.MustParse(req.GetWebhookId()),
			WorkspaceID: workspaceID,
			Url:         req.GetUrl(),
			EventTypes:  types,
		})
		if errors.Is(updateErr, pgx.ErrNoRows) {
			return ErrWebhookNotFound
		}
		if updateErr != nil {
			return fmt.Errorf("update webhook: %w", updateErr)
		}
		row = updated
		return events.Record(ctx, qtx, events.Event{
			Type:        events.WebhookUpdated,
			WorkspaceID: new(workspaceID),
			SubjectType: events.SubjectWebhook,
			SubjectID:   new(row.ID),
			Data:        map[string]any{fieldWebhookURL: row.Url, fieldWebhookEvents: row.EventTypes},
		})
	})
	if err != nil {
		return nil, err
	}
	return &webhookv1.UpdateWebhookResponse{Webhook: webhookToProto(row)}, nil
}

func (s *WebhookServer) RotateWebhookSecret(
	ctx context.Context,
	req *webhookv1.RotateWebhookSecretRequest,
) (*webhookv1.RotateWebhookSecretResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ManageWorkspaceWebhooks, workspaceID); err != nil {
		return nil, err
	}
	current, err := s.loadWebhook(ctx, workspaceID, req.GetWebhookId())
	if err != nil {
		return nil, err
	}
	secret, err := webhooksig.NewSecret()
	if err != nil {
		return nil, fmt.Errorf("generate webhook secret: %w", err)
	}
	// A workspace webhook keeps its data key for life, so sealing outside the update cannot race another rotation.
	key := secretkeys.WrappedKey{Provider: current.Provider, KeyID: current.KekID, Bytes: current.WrappedDek}
	sealed, err := webhooks.SealSecret(ctx, s.provider, current.ID, key, secret)
	if err != nil {
		return nil, keyProviderUnavailable(err)
	}
	var row genDb.Webhook
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		rotated, rotateErr := qtx.RotateWorkspaceWebhookSecret(ctx, genDb.RotateWorkspaceWebhookSecretParams{
			ID:           current.ID,
			WorkspaceID:  workspaceID,
			Secret:       sealed,
			GraceSeconds: req.GetGracePeriodSeconds(),
		})
		if errors.Is(rotateErr, pgx.ErrNoRows) {
			return ErrWebhookNotFound
		}
		if rotateErr != nil {
			return fmt.Errorf("rotate webhook secret: %w", rotateErr)
		}
		row = rotated
		return events.Record(ctx, qtx, events.Event{
			Type:        events.WebhookSecretRotated,
			WorkspaceID: new(workspaceID),
			SubjectType: events.SubjectWebhook,
			SubjectID:   new(row.ID),
			Data:        map[string]any{fieldGracePeriod: req.GetGracePeriodSeconds()},
		})
	})
	if err != nil {
		return nil, err
	}
	return &webhookv1.RotateWebhookSecretResponse{Webhook: webhookToProto(row), Secret: secret}, nil
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
		return row, ErrWebhookNotFound
	}
	if err != nil {
		return row, fmt.Errorf("get webhook: %w", err)
	}
	return row, nil
}

func (s *WebhookServer) DeleteWebhook(
	ctx context.Context,
	req *webhookv1.DeleteWebhookRequest,
) (*webhookv1.DeleteWebhookResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ManageWorkspaceWebhooks, workspaceID); err != nil {
		return nil, err
	}
	row, err := s.loadWebhook(ctx, workspaceID, req.GetWebhookId())
	if err != nil {
		return nil, err
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		deleted, deleteErr := qtx.DeleteWorkspaceWebhook(
			ctx,
			genDb.DeleteWorkspaceWebhookParams{ID: row.ID, WorkspaceID: workspaceID},
		)
		if deleteErr != nil {
			return fmt.Errorf("delete webhook: %w", deleteErr)
		}
		if deleted == 0 {
			return ErrWebhookNotFound
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
		return nil, err
	}
	return &webhookv1.DeleteWebhookResponse{}, nil
}

func (s *WebhookServer) ListWebhookDeliveries(
	ctx context.Context,
	req *webhookv1.ListWebhookDeliveriesRequest,
) (*webhookv1.ListWebhookDeliveriesResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ManageWorkspaceWebhooks, workspaceID); err != nil {
		return nil, err
	}
	row, err := s.loadWebhook(ctx, workspaceID, req.GetWebhookId())
	if err != nil {
		return nil, err
	}
	cursor, err := decodePositionCursor(req.GetPageToken())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
	}
	pageSize := normalizePageSize(req.GetPageSize())
	rows, err := s.queries.ListWebhookDeliveries(ctx, genDb.ListWebhookDeliveriesParams{
		WebhookID:     row.ID,
		Status:        req.GetStatus(),
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
		MaxRows:       pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("list webhook deliveries: %w", err)
	}
	out := make([]*webhookv1.WebhookDelivery, len(rows))
	for i, d := range rows {
		out[i] = &webhookv1.WebhookDelivery{
			Id:            d.ID.String(),
			EventId:       d.EventID.String(),
			EventType:     d.EventType,
			Status:        d.Status,
			Attempts:      d.Attempts,
			LastError:     derefString(d.LastError),
			CreatedAt:     timestamppb.New(d.CreatedAt),
			DeliveredAt:   optionalTimestamp(d.DeliveredAt),
			NextAttemptAt: timestamppb.New(d.NextAttemptAt),
		}
	}
	token := nextPageToken(rows, pageSize, func(d genDb.ListWebhookDeliveriesRow) (uuid.UUID, time.Time) {
		return d.ID, d.CreatedAt
	})
	return &webhookv1.ListWebhookDeliveriesResponse{Deliveries: out, NextPageToken: token}, nil
}

func (s *WebhookServer) RedeliverWebhookDelivery(
	ctx context.Context,
	req *webhookv1.RedeliverWebhookDeliveryRequest,
) (*webhookv1.RedeliverWebhookDeliveryResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ManageWorkspaceWebhooks, workspaceID); err != nil {
		return nil, err
	}
	row, err := s.loadWebhook(ctx, workspaceID, req.GetWebhookId())
	if err != nil {
		return nil, err
	}
	deliveryID := uuid.MustParse(req.GetDeliveryId())
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		status, lockErr := qtx.LockWebhookDeliveryStatus(ctx, genDb.LockWebhookDeliveryStatusParams{
			ID:        deliveryID,
			WebhookID: row.ID,
		})
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrDeliveryNotFound
		}
		if lockErr != nil {
			return fmt.Errorf("lock delivery: %w", lockErr)
		}
		// A pending delivery may be in flight; requeueing it would reset the attempt its sender finishes.
		if status == webhooks.StatusPending {
			return ErrDeliveryPending
		}
		if requeueErr := qtx.RequeueWebhookDelivery(ctx, deliveryID); requeueErr != nil {
			return fmt.Errorf("requeue delivery: %w", requeueErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &webhookv1.RedeliverWebhookDeliveryResponse{}, nil
}

func (s *WebhookServer) SendTestEvent(
	ctx context.Context,
	req *webhookv1.SendTestEventRequest,
) (*webhookv1.SendTestEventResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ManageWorkspaceWebhooks, workspaceID); err != nil {
		return nil, err
	}
	row, err := s.loadWebhook(ctx, workspaceID, req.GetWebhookId())
	if err != nil {
		return nil, err
	}
	if err = events.Record(ctx, s.queries, events.Event{
		Type:        events.WebhookTest,
		WorkspaceID: new(workspaceID),
		SubjectType: events.SubjectWebhook,
		SubjectID:   new(row.ID),
		Data:        map[string]any{fieldWebhookURL: row.Url},
	}); err != nil {
		return nil, err
	}
	return &webhookv1.SendTestEventResponse{}, nil
}
