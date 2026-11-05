package main

import (
	"context"
	"log/slog"
	"time"
)

func (a *Agent) sweepEnvSecrets(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.InventoryInterval)
	defer ticker.Stop()
	for {
		if err := a.applier.SweepEnvSecrets(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "failed to sweep orphaned env Secrets", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
