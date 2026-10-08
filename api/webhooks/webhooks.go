package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
)

const (
	secretPrefix      = "whsec_"
	secretBytes       = 32
	requestTimeout    = 10 * time.Second
	leaseSeconds      = 120
	claimBatch        = 50
	maxErrorLength    = 500
	maxResponseBytes  = 64 << 10
	StatusPending     = "pending"
	StatusSucceeded   = "succeeded"
	StatusFailed      = "failed"
	DefaultPollPeriod = 2 * time.Second
)

var (
	ErrBlockedAddress = errors.New("webhook address is on a private or reserved network")
	ErrInvalidSecret  = errors.New("webhook secret is malformed")
	ErrUnknownKind    = errors.New("unknown webhook kind")

	retryDelays = []time.Duration{
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
		time.Hour,
		6 * time.Hour,
		24 * time.Hour,
	}

	sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")
)

func NewSecret() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	return secretPrefix + base64.StdEncoding.EncodeToString(b), nil
}

func decodeSecret(secret string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(secret, secretPrefix)
	if !ok {
		return nil, ErrInvalidSecret
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != secretBytes {
		return nil, ErrInvalidSecret
	}
	return key, nil
}

func Sign(secret, id string, timestamp time.Time, body []byte) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + strconv.FormatInt(timestamp.Unix(), 10) + "."))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

func blocked(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() ||
		sharedAddressSpace.Contains(addr)
}

func NewClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: requestTimeout}
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
		TLSHandshakeTimeout:   requestTimeout,
		ResponseHeaderTimeout: requestTimeout,
		MaxIdleConns:          claimBatch,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
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
	workspaceClient *http.Client
	installClient   *http.Client
	now             func() time.Time
}

func NewDispatcher(queries genDb.Querier, workspaceClient, installClient *http.Client) *Dispatcher {
	return &Dispatcher{queries: queries, workspaceClient: workspaceClient, installClient: installClient, now: time.Now}
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

func (d *Dispatcher) Run(ctx context.Context, period time.Duration) {
	ticker := time.NewTicker(period)
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
		LeaseSeconds: leaseSeconds,
		MaxRows:      claimBatch,
	})
	if err != nil {
		return 0, fmt.Errorf("claim webhook deliveries: %w", err)
	}
	for _, c := range claimed {
		d.deliver(ctx, c.ID, int(c.Attempts))
	}
	return len(claimed), nil
}

func (d *Dispatcher) deliver(ctx context.Context, id uuid.UUID, attempt int) {
	row, err := d.queries.GetWebhookDeliveryPayload(ctx, id)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load webhook delivery", "deliveryId", id, "error", err)
		return
	}
	code, sendErr := d.send(ctx, id, row)
	params := genDb.FinishWebhookDeliveryParams{ID: id, NextAttemptAt: d.now()}
	if code != 0 {
		statusCode := int32(code)
		params.LastStatusCode = &statusCode
	}
	switch {
	case sendErr == nil:
		params.Status = StatusSucceeded
		delivered := d.now()
		params.DeliveredAt = &delivered
	case attempt > len(retryDelays):
		params.Status = StatusFailed
	default:
		params.Status = StatusPending
		params.NextAttemptAt = d.now().Add(retryDelays[attempt-1])
	}
	if sendErr != nil {
		message := sendErr.Error()
		if len(message) > maxErrorLength {
			message = message[:maxErrorLength]
		}
		params.LastError = &message
		slog.InfoContext(ctx, "webhook delivery failed", "deliveryId", id, "attempt", attempt, "error", sendErr)
	}
	if err := d.queries.FinishWebhookDelivery(ctx, params); err != nil {
		slog.ErrorContext(ctx, "failed to record webhook delivery", "deliveryId", id, "error", err)
	}
}

func (d *Dispatcher) send(ctx context.Context, id uuid.UUID, row genDb.GetWebhookDeliveryPayloadRow) (int, error) {
	body, err := json.Marshal(payloadFrom(row))
	if err != nil {
		return 0, fmt.Errorf("encode payload: %w", err)
	}
	now := d.now()
	signature, err := Sign(row.Secret, id.String(), now, body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, row.Url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Loco-Webhooks")
	req.Header.Set("Webhook-Id", id.String())
	req.Header.Set("Webhook-Timestamp", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("Webhook-Signature", signature)
	client, err := d.clientFor(row.Kind)
	if err != nil {
		return 0, err
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(res.Body, maxResponseBytes)); err != nil {
		slog.DebugContext(ctx, "failed to drain webhook response", "error", err)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return res.StatusCode, fmt.Errorf("endpoint returned %s", res.Status)
	}
	return res.StatusCode, nil
}
