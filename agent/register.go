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

	"github.com/team-loco/loco/agent/pkg/identity"
	"github.com/team-loco/loco/agent/pkg/rpcretry"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/internal/bearer"
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
	callCtx := ctx
	if stored.ClusterID == "" {
		token, tokenErr := a.joinToken()
		if tokenErr != nil {
			return tokenErr
		}
		if token != "" {
			req.Enrollment = &agentv1.Enrollment{JoinToken: token, PublicKey: stored.PublicKey()}
		} else {
			// Without a token, a key enrolled before the agent saved its cluster ID recovers that ID.
			req.RecoverClusterId = true
			recovery := identity.Identity{Key: stored.Key, ClusterID: key}
			assertion, signErr := recovery.Assertion(time.Now(), a.cfg.AssertionLifetime)
			if signErr != nil {
				return signErr
			}
			recoveryCtx, info := connect.NewClientContext(ctx)
			info.RequestHeader().Set("Authorization", bearer.Header(assertion))
			callCtx = recoveryCtx
		}
	} else {
		a.credentials.Use(stored)
	}

	resp, err := a.client.Register(callCtx, req)
	if err != nil {
		if stored.ClusterID == "" && connect.CodeOf(err) == connect.CodeUnauthenticated {
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
		"recovered", req.GetRecoverClusterId(),
		"heartbeat_interval", a.heartbeatInterval,
		"domains", req.GetDomains(),
	)
	return nil
}

// joinToken is the configured join token, or "" when none has been delivered yet.
func (a *Agent) joinToken() (string, error) {
	if a.cfg.JoinTokenFile == "" {
		return a.cfg.JoinToken, nil
	}
	data, err := os.ReadFile(a.cfg.JoinTokenFile)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read join token file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}
