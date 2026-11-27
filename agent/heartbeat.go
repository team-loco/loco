package main

import (
	"context"
	"log/slog"
	"time"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func (a *Agent) sendHeartbeats(ctx context.Context, enqueue func(*agentv1.SyncRequest)) {
	ticker := time.NewTicker(a.heartbeatInterval)
	defer ticker.Stop()
	for {
		capacity, chunks, err := a.inspector.CapacityReport(ctx)
		if err != nil {
			slog.WarnContext(ctx, "failed to read node capacity", "error", err)
		}
		heartbeat := a.heartbeat(ctx, capacity)
		enqueue(&agentv1.SyncRequest{Message: &agentv1.SyncRequest_Heartbeat{Heartbeat: heartbeat}})
		for _, chunk := range chunks {
			enqueue(&agentv1.SyncRequest{Message: &agentv1.SyncRequest_CapacityReport{CapacityReport: chunk}})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) heartbeat(ctx context.Context, capacity *agentv1.AgentCapacity) *agentv1.Heartbeat {
	health := a.getHealth(ctx)
	return &agentv1.Heartbeat{
		Capacity: capacity,
		Health:   health,
		Role:     a.cfg.Role,
		Domains:  a.servedDomains(ctx),
	}
}

// servedDomains returns the app domains the gateway serves.
func (a *Agent) servedDomains(ctx context.Context) []string {
	served, err := a.domains.Served(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to read the domains the gateway serves", "error", err)
		return nil
	}
	return served
}

func (a *Agent) getCapacity(ctx context.Context) *agentv1.AgentCapacity {
	capacity, err := a.inspector.Capacity(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to read cluster capacity", "error", err)
		return nil
	}
	return capacity
}

func (a *Agent) getHealth(ctx context.Context) *agentv1.AgentHealth {
	health := a.inspector.Health(ctx)
	if !health.GetKubernetesHealthy() || !health.GetControllerHealthy() {
		slog.WarnContext(ctx, "cluster unhealthy", "message", health.GetMessage())
	}
	return health
}
