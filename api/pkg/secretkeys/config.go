package secretkeys

import (
	"context"
	"fmt"
)

// New returns the transit provider cfg configures. It renews its token in the background
// until ctx is done, and caches unwrapped data keys for cfg.CacheTTL.
func New(ctx context.Context, cfg TransitConfig) (Provider, error) {
	transit, err := NewTransit(cfg)
	if err != nil {
		return nil, fmt.Errorf("transit secrets key provider: %w", err)
	}
	go transit.renewToken(ctx)
	return NewDEKCache(transit, cfg.CacheTTL), nil
}
