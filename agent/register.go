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

const rejoinHint = "restore a removed cluster with locoadm cluster restore, " +
	"or create a lost one again with locoadm cluster create --kube-context to deliver a new join token"

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
		Region:       a.cfg.Region,
	}
	resp, err := a.announce(ctx, stored, req)
	if err != nil {
		return err
	}
	// Only an enrollment with a join token, or recovery of an unsaved ID, may name a new cluster ID.
	if resp.GetClusterId() != stored.ClusterID {
		if stored.ClusterID != "" && req.GetEnrollment() == nil {
			return fmt.Errorf("%w: %s, enrolled as %s", errClusterIDChanged, resp.GetClusterId(), stored.ClusterID)
		}
		if err := a.identities.SaveClusterID(ctx, resp.GetClusterId()); err != nil {
			return err
		}
		stored.ClusterID = resp.GetClusterId()
	}
	a.credentials.Use(stored)

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

// announce registers as the stored cluster ID. Without one, or when the control plane no longer
// accepts it, the agent enrolls with the configured join token.
func (a *Agent) announce(
	ctx context.Context,
	stored identity.Identity,
	req *agentv1.RegisterRequest,
) (*agentv1.RegisterResponse, error) {
	if stored.ClusterID != "" {
		resp, err := a.registerAs(ctx, stored, req)
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			return resp, err
		}
	}
	token, err := a.joinToken()
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Enrollment = &agentv1.Enrollment{JoinToken: token, PublicKey: stored.PublicKey()}
		return notEnrolledIfRejected(a.registerCall(ctx, req))
	}
	if stored.ClusterID != "" {
		return nil, fmt.Errorf(
			"%w: the control plane rejected cluster %s; %s", errNotEnrolled, stored.ClusterID, rejoinHint,
		)
	}
	// Without a token, a key enrolled before the agent saved its cluster ID recovers that ID.
	req.RecoverClusterId = true
	recovery := identity.Identity{Key: stored.Key, ClusterID: req.GetClusterKey()}
	return notEnrolledIfRejected(a.registerAs(ctx, recovery, req))
}

// registerAs authenticates the call as signer, before the agent's credentials are in use.
func (a *Agent) registerAs(
	ctx context.Context,
	signer identity.Identity,
	req *agentv1.RegisterRequest,
) (*agentv1.RegisterResponse, error) {
	assertion, err := signer.Assertion(time.Now(), a.cfg.AssertionLifetime)
	if err != nil {
		return nil, err
	}
	callCtx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", bearer.Header(assertion))
	return a.registerCall(callCtx, req)
}

func (a *Agent) registerCall(ctx context.Context, req *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
	resp, err := a.client.Register(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("register RPC failed: %w", err)
	}
	return resp, nil
}

func notEnrolledIfRejected(resp *agentv1.RegisterResponse, err error) (*agentv1.RegisterResponse, error) {
	if connect.CodeOf(err) == connect.CodeUnauthenticated {
		return nil, errNotEnrolled
	}
	return resp, err
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
