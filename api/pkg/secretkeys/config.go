package secretkeys

import (
	"context"
	"time"

	"github.com/team-loco/loco/api/pkg/transit"
)

type Config struct {
	Key      string
	CacheTTL time.Duration
}

// Validate reports the first invalid field of cfg.
func (cfg Config) Validate() error {
	if err := transit.ValidateKeyName(cfg.Key); err != nil {
		return err
	}
	if cfg.CacheTTL <= 0 {
		return ErrCacheTTL
	}
	return nil
}

// New checks that cfg.Key is an aes256-gcm96 key created with derived=true and returns a
// provider that wraps data keys with it through client and caches unwrapped data keys for
// cfg.CacheTTL.
func New(ctx context.Context, client *transit.Client, cfg Config) (*DEKCache, error) {
	provider := transitProvider{key: client.EncryptionKey(cfg.Key)}
	if _, err := provider.KeyID(ctx); err != nil {
		return nil, err
	}
	return NewDEKCache(provider, cfg.CacheTTL), nil
}
