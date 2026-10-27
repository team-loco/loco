package secretkeys

import (
	"context"
	"fmt"
)

// New logs in to Transit, then checks that cfg.Key is an aes256-gcm96 key created with
// derived=true and that signingKey is an ed25519 key with a public key, all within
// cfg.StartupTimeout. It returns the transit provider, which caches unwrapped data keys
// for cfg.CacheTTL, and a signer for signingKey on the same mount and token, and renews
// the token in the background until ctx is done.
func New(ctx context.Context, cfg TransitConfig, signingKey string) (Provider, *TransitSigner, error) {
	transit, err := NewTransit(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("transit secrets key provider: %w", err)
	}
	startup, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancel()
	lease, err := transit.logIn(startup)
	if err != nil {
		return nil, nil, fmt.Errorf("log in to transit at %s: %w", cfg.Address, err)
	}
	if _, err := transit.KeyID(startup); err != nil {
		return nil, nil, fmt.Errorf("secrets key %s/%s: %w", cfg.Mount, cfg.Key, err)
	}
	signer := transit.Signer(signingKey)
	if _, err := signer.PublicKeys(startup); err != nil {
		return nil, nil, fmt.Errorf("ingest key %s/%s: %w", cfg.Mount, signingKey, err)
	}
	go transit.renewToken(ctx, lease)
	return NewDEKCache(transit, cfg.CacheTTL), signer, nil
}
