package webhooks

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/advisorylock"
	"github.com/team-loco/loco/api/pkg/secretkeys"
)

var (
	ErrNtfyServer = errors.New(
		"ntfy server must be an http or https URL with a host and no credentials, query or fragment",
	)
	ErrNtfyTopic     = errors.New("ntfy topic must be 1 to 64 letters, digits, underscores or dashes")
	ErrNtfyEventType = errors.New("ntfy event types must not contain an empty name")
	ErrNtfyUnset     = errors.New("ntfy server and topic must be set together")

	ntfyTopic = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// DefaultNtfyEventTypes are the events notified when the operator lists none.
var DefaultNtfyEventTypes = []string{
	events.BuildFailed,
	events.DeploymentFailed,
	events.DeploymentCrashed,
	events.ClusterSilent,
	events.ClusterRecovered,
	events.ClusterHealthChanged,
	events.OrgSuspended,
}

// NtfyConfig is one ntfy topic that receives notifications for the listed event types.
type NtfyConfig struct {
	// TopicURL is the server URL followed by the topic.
	TopicURL   string
	Token      string
	EventTypes []string
}

// NewNtfyConfig returns nil when neither server nor topic is set, which turns notifications off.
func NewNtfyConfig(server, topic, token string, eventTypes []string) (*NtfyConfig, error) {
	if server == "" && topic == "" {
		return nil, nil
	}
	if server == "" || topic == "" {
		return nil, ErrNtfyUnset
	}
	u, err := url.Parse(server)
	if err != nil || !validNtfyServer(u) {
		return nil, ErrNtfyServer
	}
	if !ntfyTopic.MatchString(topic) {
		return nil, ErrNtfyTopic
	}
	if slices.Contains(eventTypes, "") {
		return nil, ErrNtfyEventType
	}
	if len(eventTypes) == 0 {
		eventTypes = DefaultNtfyEventTypes
	}
	topicURL, err := url.JoinPath(server, topic)
	if err != nil {
		return nil, ErrNtfyServer
	}
	return &NtfyConfig{
		TopicURL:   topicURL,
		Token:      token,
		EventTypes: eventTypes,
	}, nil
}

// validNtfyServer accepts an http or https URL with a host and no credentials, query or fragment.
func validNtfyServer(u *url.URL) bool {
	if u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS {
		return false
	}
	hasHost := u.Hostname() != ""
	plain := u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
	return hasHost && plain
}

// SyncNtfy makes the install's ntfy target match cfg, and removes it when cfg is nil. The access token
// is sealed under a fresh data key wrapped by provider. The target keeps its id and its deliveries.
func SyncNtfy(ctx context.Context, pool *pgxpool.Pool, provider secretkeys.Provider, cfg *NtfyConfig) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		q := genDb.New(tx)
		if err := q.LockInstallWebhooks(ctx, advisorylock.InstallWebhookSync); err != nil {
			return fmt.Errorf("lock install webhooks: %w", err)
		}
		if cfg == nil {
			return q.DeleteNtfyWebhook(ctx)
		}
		id, err := q.GetNtfyWebhookID(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			id, err = uuid.NewV7()
		}
		if err != nil {
			return fmt.Errorf("get ntfy target: %w", err)
		}
		key, sealed, err := SealSecretWithNewKey(ctx, provider, id, cfg.Token)
		if err != nil {
			return fmt.Errorf("ntfy target: %w", err)
		}
		return q.UpsertNtfyWebhook(ctx, genDb.UpsertNtfyWebhookParams{
			ID:         id,
			Url:        cfg.TopicURL,
			Provider:   key.Provider,
			KekID:      key.KeyID,
			WrappedDek: key.Bytes,
			Secret:     sealed,
			EventTypes: cfg.EventTypes,
		})
	})
}
