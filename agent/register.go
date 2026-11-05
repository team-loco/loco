package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

var (
	errInvalidHeartbeatInterval = errors.New("control plane sent no positive heartbeat interval")
	errNotEnrolled              = errors.New("agent is not enrolled and LOCO_JOIN_TOKEN is not set")
	errClusterIDChanged         = errors.New("control plane registered the agent as a different cluster")
)

// register announces the agent to the control plane.
func (a *Agent) register(ctx context.Context) error {
	key, err := a.inspector.Key(ctx)
	if err != nil {
		return err
	}
	stored, err := a.identities.Ensure(ctx)
	if err != nil {
		return err
	}
	capacity := a.getCapacity(ctx)
	req := &agentv1.RegisterRequest{
		AgentVersion:  a.cfg.AgentVersion,
		ClusterKey:    key,
		Capacity:      capacity,
		BuildsEnabled: a.buildsEnabled,
		Domains:       a.servedDomains(ctx),
	}
	if stored.ClusterID == "" {
		if a.cfg.JoinToken == "" {
			return errNotEnrolled
		}
		req.Enrollment = &agentv1.Enrollment{JoinToken: a.cfg.JoinToken, PublicKey: stored.PublicKey()}
	} else {
		a.credentials.Use(stored)
	}

	resp, err := a.client.Register(ctx, req)
	if err != nil {
		return fmt.Errorf("register RPC failed: %w", err)
	}
	if stored.ClusterID == "" {
		if err := a.identities.SaveClusterID(ctx, resp.GetClusterId()); err != nil {
			return err
		}
		stored.ClusterID = resp.GetClusterId()
		a.credentials.Use(stored)
	}
	if resp.GetClusterId() != stored.ClusterID {
		return fmt.Errorf("%w: %s, enrolled as %s", errClusterIDChanged, resp.GetClusterId(), stored.ClusterID)
	}

	heartbeatInterval := resp.GetHeartbeatInterval().AsDuration()
	if heartbeatInterval <= 0 {
		return fmt.Errorf("%w: %s", errInvalidHeartbeatInterval, heartbeatInterval)
	}
	a.heartbeatInterval = heartbeatInterval
	slog.InfoContext(ctx, "registered with control plane",
		"cluster_id", stored.ClusterID,
		"enrolled", req.GetEnrollment() != nil,
		"heartbeat_interval", a.heartbeatInterval,
		"domains", req.GetDomains(),
	)
	return nil
}
