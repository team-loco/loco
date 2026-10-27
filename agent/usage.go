package main

import (
	"context"
	"log/slog"
	"time"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func (a *Agent) reportUsage(ctx context.Context, enqueue func(*agentv1.SyncRequest)) {
	ticker := time.NewTicker(a.cfg.UsageInterval)
	defer ticker.Stop()
	for {
		report, err := a.usage.Report(ctx)
		if err != nil {
			slog.WarnContext(ctx, "failed to build usage report", "error", err)
		} else {
			enqueue(&agentv1.SyncRequest{Message: &agentv1.SyncRequest_Usage{Usage: report}})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-a.usage.Wake():
		}
	}
}
