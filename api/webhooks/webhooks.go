package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/tracing"
	"github.com/team-loco/loco/api/pkg/webhooksig"
	"github.com/team-loco/loco/internal/clip"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	leaseMargin     = 30 * time.Second
	maxErrorLength  = 500
	statusPending   = "pending"
	statusSucceeded = "succeeded"
	statusFailed    = "failed"

	deliverySpan          = "webhook.deliver"
	deliveryIDAttr        = "loco.webhook.delivery_id"
	deliveryAttemptAttr   = "loco.webhook.attempt"
	deliveryKindAttr      = "loco.webhook.kind"
	deliveryEventTypeAttr = "loco.event.type"
	deliveryStatusAttr    = "loco.webhook.status"
)

var (
	ErrBlockedAddress = errors.New("webhook address is on a private or reserved network")
	ErrUnknownKind    = errors.New("unknown webhook kind")

	sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")
)

// Config sets how the dispatcher claims, sends and retries deliveries.
type Config struct {
	RequestTimeout time.Duration
	ClaimBatch     int32
	// RetryDelays[n] is the wait after failed attempt n+1; the delivery fails after len(RetryDelays)+1 attempts.
	RetryDelays  []time.Duration
	PollInterval time.Duration
}

// lease outlasts sending a whole claimed batch, so no other dispatcher reclaims a delivery still in flight.
func (c Config) lease() time.Duration {
	return time.Duration(c.ClaimBatch)*c.RequestTimeout + leaseMargin
}

func blocked(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() ||
		sharedAddressSpace.Contains(addr)
}

func NewClient(cfg Config, allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: cfg.RequestTimeout}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: %s", ErrBlockedAddress, address)
			}
			if blocked(ap.Addr()) {
				return fmt.Errorf("%w: %s", ErrBlockedAddress, ap.Addr())
			}
			return nil
		}
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   cfg.RequestTimeout,
		ResponseHeaderTimeout: cfg.RequestTimeout,
		MaxIdleConns:          int(cfg.ClaimBatch),
	}
	return &http.Client{
		Transport: transport,
		Timeout:   cfg.RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type Actor struct {
	Type string     `json:"type"`
	ID   *uuid.UUID `json:"id,omitempty"`
}

type Subject struct {
	Type string     `json:"type,omitempty"`
	ID   *uuid.UUID `json:"id,omitempty"`
}

type Payload struct {
	ID          uuid.UUID       `json:"id"`
	Type        string          `json:"type"`
	Seq         int64           `json:"seq"`
	CreatedAt   time.Time       `json:"createdAt"`
	OrgID       *uuid.UUID      `json:"orgId,omitempty"`
	WorkspaceID *uuid.UUID      `json:"workspaceId,omitempty"`
	Actor       Actor           `json:"actor"`
	Subject     Subject         `json:"subject"`
	RequestID   string          `json:"requestId,omitempty"`
	Data        json.RawMessage `json:"data"`
}

func payloadFrom(row genDb.GetWebhookDeliveryPayloadRow) Payload {
	p := Payload{
		ID:          row.EventID,
		Type:        row.Type,
		Seq:         row.Seq,
		CreatedAt:   row.CreatedAt,
		OrgID:       row.OrgID,
		WorkspaceID: row.WorkspaceID,
		Actor:       Actor{Type: row.ActorType, ID: row.ActorID},
		Subject:     Subject{ID: row.SubjectID},
		Data:        json.RawMessage(row.Data),
	}
	if row.SubjectType != nil {
		p.Subject.Type = *row.SubjectType
	}
	if row.RequestID != nil {
		p.RequestID = *row.RequestID
	}
	return p
}

type Dispatcher struct {
	queries         genDb.Querier
	cfg             Config
	workspaceClient *http.Client
	installClient   *http.Client
	now             func() time.Time
}

func NewDispatcher(queries genDb.Querier, cfg Config, workspaceClient, installClient *http.Client) *Dispatcher {
	return &Dispatcher{
		queries:         queries,
		cfg:             cfg,
		workspaceClient: workspaceClient,
		installClient:   installClient,
		now:             time.Now,
	}
}

func (d *Dispatcher) clientFor(kind genDb.WebhookKind) (*http.Client, error) {
	switch kind {
	case genDb.WebhookKindWorkspace:
		return d.workspaceClient, nil
	case genDb.WebhookKindInstall:
		return d.installClient, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
}

func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.cfg.PollInterval)
	defer ticker.Stop()
	for {
		if _, err := d.DispatchDue(ctx); err != nil {
			slog.ErrorContext(ctx, "webhook dispatch failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Dispatcher) DispatchDue(ctx context.Context) (int, error) {
	claimed, err := d.queries.ClaimWebhookDeliveries(ctx, genDb.ClaimWebhookDeliveriesParams{
		LeaseSeconds: int32(d.cfg.lease() / time.Second),
		MaxRows:      d.cfg.ClaimBatch,
	})
	if err != nil {
		return 0, fmt.Errorf("claim webhook deliveries: %w", err)
	}
	for _, c := range claimed {
		select {
		case <-ctx.Done():
			return len(claimed), nil
		default:
		}
		d.deliver(ctx, c.ID, int(c.Attempts))
	}
	return len(claimed), nil
}

func (d *Dispatcher) deliver(ctx context.Context, id uuid.UUID, attempt int) {
	deliveryID := attribute.String(deliveryIDAttr, id.String())
	attempted := attribute.Int(deliveryAttemptAttr, attempt)
	consumer := trace.WithSpanKind(trace.SpanKindConsumer)
	attributes := trace.WithAttributes(deliveryID, attempted)
	ctx, span := tracing.StartRoot(ctx, deliverySpan, consumer, attributes)
	row, err := d.queries.GetWebhookDeliveryPayload(ctx, id)
	if err != nil {
		tracing.End(span, err)
		slog.ErrorContext(ctx, "failed to load webhook delivery", "deliveryId", id, "error", err)
		return
	}
	kind := attribute.String(deliveryKindAttr, string(row.Kind))
	eventType := attribute.String(deliveryEventTypeAttr, row.Type)
	span.SetAttributes(kind, eventType)
	if row.Traceparent != nil {
		if link, ok := tracing.LinkTo(*row.Traceparent); ok {
			span.AddLink(link)
		}
	}
	code, sendErr := d.send(ctx, id, row)
	params := genDb.FinishWebhookDeliveryParams{ID: id, Attempts: int32(attempt), NextAttemptAt: d.now()}
	if code != 0 {
		statusCode := int32(code)
		params.LastStatusCode = &statusCode
	}
	switch {
	case sendErr == nil:
		params.Status = statusSucceeded
		delivered := d.now()
		params.DeliveredAt = &delivered
	case attempt > len(d.cfg.RetryDelays):
		params.Status = statusFailed
	default:
		params.Status = statusPending
		params.NextAttemptAt = d.now().Add(d.cfg.RetryDelays[attempt-1])
	}
	if sendErr != nil {
		message := clip.Bytes(sendErr.Error(), maxErrorLength)
		params.LastError = &message
		slog.InfoContext(ctx, "webhook delivery failed", "deliveryId", id, "attempt", attempt, "error", sendErr)
	}
	span.SetAttributes(attribute.String(deliveryStatusAttr, params.Status))
	rows, err := d.queries.FinishWebhookDelivery(context.WithoutCancel(ctx), params)
	switch {
	case err != nil:
		slog.ErrorContext(ctx, "failed to record webhook delivery", "deliveryId", id, "error", err)
	case rows == 0:
		slog.ErrorContext(
			ctx, "webhook delivery was reclaimed before its result was recorded", "deliveryId", id, "attempt", attempt,
		)
	}
	tracing.End(span, sendErr)
}

func (d *Dispatcher) send(ctx context.Context, id uuid.UUID, row genDb.GetWebhookDeliveryPayloadRow) (int, error) {
	body, err := json.Marshal(payloadFrom(row))
	if err != nil {
		return 0, fmt.Errorf("encode payload: %w", err)
	}
	req, err := webhooksig.NewSignedRequest(ctx, row.Url, row.Secret, id.String(), body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Loco-Webhooks")
	if row.Kind == genDb.WebhookKindInstall {
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	}
	client, err := d.clientFor(row.Kind)
	if err != nil {
		return 0, err
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(res.Body, webhooksig.MaxResponseBytes)); err != nil {
		slog.DebugContext(ctx, "failed to drain webhook response", "error", err)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return res.StatusCode, fmt.Errorf("endpoint returned %s", res.Status)
	}
	return res.StatusCode, nil
}
