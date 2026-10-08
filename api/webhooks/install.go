package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
)

const (
	installSyncLockKey int64 = 0x6c6f636f0002
	schemeHTTP               = "http"
	schemeHTTPS              = "https"
)

var (
	ErrInstallWebhookURL       = errors.New("webhook url must be http or https with a host and no credentials")
	ErrDuplicateInstallWebhook = errors.New("webhook url is configured twice")
)

type InstallWebhook struct {
	URL        string   `json:"url"`
	Secret     string   `json:"secret"`
	EventTypes []string `json:"eventTypes"`
}

func ParseInstallWebhooks(raw string) ([]InstallWebhook, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var hooks []InstallWebhook
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&hooks); err != nil {
		return nil, fmt.Errorf("decode install webhooks: %w", err)
	}
	seen := map[string]bool{}
	for i := range hooks {
		hook := &hooks[i]
		if err := hook.validate(); err != nil {
			return nil, fmt.Errorf("install webhook %d: %w", i, err)
		}
		if seen[hook.URL] {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateInstallWebhook, hook.URL)
		}
		seen[hook.URL] = true
		if hook.EventTypes == nil {
			hook.EventTypes = []string{}
		}
	}
	return hooks, nil
}

func (h *InstallWebhook) validate() error {
	u, err := url.Parse(h.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) {
		return ErrInstallWebhookURL
	}
	if _, err := decodeSecret(h.Secret); err != nil {
		return err
	}
	return nil
}

func SyncInstallWebhooks(ctx context.Context, pool *pgxpool.Pool, hooks []InstallWebhook) error {
	urls := make([]string, len(hooks))
	for i, hook := range hooks {
		urls[i] = hook.URL
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		q := genDb.New(tx)
		if err := q.LockInstallWebhooks(ctx, installSyncLockKey); err != nil {
			return fmt.Errorf("lock install webhooks: %w", err)
		}
		if _, err := q.DeleteInstallWebhooksExcept(ctx, urls); err != nil {
			return fmt.Errorf("delete install webhooks: %w", err)
		}
		for _, hook := range hooks {
			if err := q.UpsertInstallWebhook(ctx, genDb.UpsertInstallWebhookParams{
				Url:        hook.URL,
				Secret:     hook.Secret,
				EventTypes: hook.EventTypes,
			}); err != nil {
				return fmt.Errorf("upsert install webhook %s: %w", hook.URL, err)
			}
		}
		return nil
	})
}
