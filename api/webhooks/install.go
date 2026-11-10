package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/advisorylock"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/webhooksig"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
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
	return webhooksig.ValidateSecret(h.Secret)
}

// SyncInstallWebhooks makes the install webhooks match hooks, sealing each secret under a fresh
// data key wrapped by provider. A webhook whose URL stays keeps its id and its deliveries.
func SyncInstallWebhooks(
	ctx context.Context,
	pool *pgxpool.Pool,
	provider secretkeys.Provider,
	hooks []InstallWebhook,
) error {
	urls := make([]string, len(hooks))
	for i, hook := range hooks {
		urls[i] = hook.URL
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		q := genDb.New(tx)
		if err := q.LockInstallWebhooks(ctx, advisorylock.InstallWebhookSync); err != nil {
			return fmt.Errorf("lock install webhooks: %w", err)
		}
		if _, err := q.DeleteInstallWebhooksExcept(ctx, urls); err != nil {
			return fmt.Errorf("delete install webhooks: %w", err)
		}
		existing, err := q.ListInstallWebhookIDs(ctx)
		if err != nil {
			return fmt.Errorf("list install webhooks: %w", err)
		}
		ids := make(map[string]uuid.UUID, len(existing))
		for _, row := range existing {
			ids[row.Url] = row.ID
		}
		for _, hook := range hooks {
			id, ok := ids[hook.URL]
			if !ok {
				if id, err = uuid.NewV7(); err != nil {
					return fmt.Errorf("generate install webhook id: %w", err)
				}
			}
			key, sealed, sealErr := SealSecretWithNewKey(ctx, provider, id, hook.Secret)
			if sealErr != nil {
				return fmt.Errorf("install webhook %s: %w", hook.URL, sealErr)
			}
			if err := q.UpsertInstallWebhook(ctx, genDb.UpsertInstallWebhookParams{
				ID:         id,
				Url:        hook.URL,
				Provider:   key.Provider,
				KekID:      key.KeyID,
				WrappedDek: key.Bytes,
				Secret:     sealed,
				EventTypes: hook.EventTypes,
			}); err != nil {
				return fmt.Errorf("upsert install webhook %s: %w", hook.URL, err)
			}
		}
		return nil
	})
}
