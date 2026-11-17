package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/agent/pkg/rpcretry"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

var (
	errInvalidHeartbeatInterval = errors.New("control plane sent no positive heartbeat interval")
	errNotEnrolled              = errors.New("waiting for a valid join token")
	errClusterIDChanged         = errors.New("control plane registered the agent as a different cluster")
)

func (a *Agent) waitForRegistration(ctx context.Context) error {
	backoff := newReconnectBackoff(a.cfg)
	for {
		err := a.register(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, errNotEnrolled) {
			var rpcErr *connect.Error
			if !errors.As(err, &rpcErr) || !rpcretry.Retryable(err) {
				return err
			}
		}
		delay := backoff.Step()
		slog.WarnContext(ctx, "waiting for agent registration", "error", err, "delay", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

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
		AgentVersion: a.cfg.AgentVersion,
		ClusterKey:   key,
		Capacity:     capacity,
		Role:         a.cfg.Role,
		Domains:      a.servedDomains(ctx),
	}
	if stored.ClusterID == "" {
		token := a.cfg.JoinToken
		if a.cfg.JoinTokenFile != "" {
			data, readErr := os.ReadFile(a.cfg.JoinTokenFile)
			if errors.Is(readErr, os.ErrNotExist) {
				return errNotEnrolled
			}
			if readErr != nil {
				return fmt.Errorf("read join token file: %w", readErr)
			}
			token = strings.TrimSpace(string(data))
		}
		if token == "" {
			return errNotEnrolled
		}
		req.Enrollment = &agentv1.Enrollment{JoinToken: token, PublicKey: stored.PublicKey()}
	} else {
		a.credentials.Use(stored)
	}

	resp, err := a.client.Register(ctx, req)
	if err != nil {
		if req.GetEnrollment() != nil && connect.CodeOf(err) == connect.CodeUnauthenticated {
			return errNotEnrolled
		}
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
