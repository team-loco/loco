package secretkeys

import (
	"context"
	"fmt"
)

// New returns the transit provider cfg configures and a signer for signingKey on the same
// Transit mount and token. It renews the token in the background until ctx is done, and
// caches unwrapped data keys for cfg.CacheTTL.
func New(ctx context.Context, cfg TransitConfig, signingKey string) (Provider, *TransitSigner, error) {
	transit, err := NewTransit(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("transit secrets key provider: %w", err)
	}
	go transit.renewToken(ctx)
	return NewDEKCache(transit, cfg.CacheTTL), transit.Signer(signingKey), nil
}
