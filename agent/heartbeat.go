package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/team-loco/loco/agent/pkg/cluster"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

var errBuildSupportChanged = errors.New("the cluster's build support changed; restarting to pick it up")

func (a *Agent) sendHeartbeats(ctx context.Context, enqueue func(*agentv1.SyncRequest)) {
	ticker := time.NewTicker(a.heartbeatInterval)
	defer ticker.Stop()
	for {
		heartbeat := a.heartbeat(ctx)
		enqueue(&agentv1.SyncRequest{Message: &agentv1.SyncRequest_Heartbeat{Heartbeat: heartbeat}})
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) heartbeat(ctx context.Context) *agentv1.Heartbeat {
	capacity := a.getCapacity(ctx)
	health := a.getHealth(ctx)
	served := a.servedDomains(ctx)
	return &agentv1.Heartbeat{
		Capacity:      capacity,
		Health:        health,
		BuildsEnabled: a.buildsEnabled,
		Domains:       served,
	}
}

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

func watchBuildSupport(
	ctx context.Context,
	inspector *cluster.Inspector,
	started bool,
	interval time.Duration,
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			enabled, err := inspector.BuildsEnabled(ctx)
			if err != nil {
				slog.WarnContext(ctx, "failed to check whether the cluster runs builds", "error", err)
				continue
			}
			if enabled != started {
				return fmt.Errorf("%w: builds_enabled went from %t to %t", errBuildSupportChanged, started, enabled)
			}
		}
	}
}
