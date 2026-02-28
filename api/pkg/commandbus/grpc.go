package commandbus

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// GRPCCommandBus dispatches commands via gRPC streams to connected agents.
// Commands flow through the bidirectional CommandStream RPC.
type GRPCCommandBus struct {
	mu         sync.RWMutex
	agents     map[string]*agentConn   // clusterID -> connection
	pending    map[string]*Command     // commandID -> command (for retry)
	maxRetries int
}

type agentConn struct {
	clusterID string
	cmdChan   chan *Command
	cancel    context.CancelFunc
}

// NewGRPCCommandBus creates a new gRPC-based command bus.
func NewGRPCCommandBus(cfg *Config) *GRPCCommandBus {
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	return &GRPCCommandBus{
		agents:     make(map[string]*agentConn),
		pending:    make(map[string]*Command),
		maxRetries: maxRetries,
	}
}

// Send dispatches a command to the target cluster's agent.
func (b *GRPCCommandBus) Send(ctx context.Context, cmd *Command) error {
	b.mu.RLock()
	agent, ok := b.agents[cmd.ClusterID]
	b.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no agent connected for cluster %s", cmd.ClusterID)
	}

	// Track for potential retry
	b.mu.Lock()
	b.pending[cmd.ID] = cmd
	b.mu.Unlock()

	select {
	case agent.cmdChan <- cmd:
		slog.Debug("command sent to agent", "command_id", cmd.ID, "cluster_id", cmd.ClusterID, "type", cmd.Type)
		return nil
	case <-ctx.Done():
		// Remove from pending if we couldn't send
		b.mu.Lock()
		delete(b.pending, cmd.ID)
		b.mu.Unlock()
		return ctx.Err()
	}
}

// Receive returns a channel of commands for a cluster.
// This is called by the AgentService when an agent connects via CommandStream.
func (b *GRPCCommandBus) Receive(ctx context.Context, clusterID string) (<-chan *Command, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// If agent already connected, return existing channel
	if agent, ok := b.agents[clusterID]; ok {
		slog.Warn("agent already connected, returning existing channel", "cluster_id", clusterID)
		return agent.cmdChan, nil
	}

	// Create new channel for this agent
	cmdChan := make(chan *Command, 100)
	connCtx, cancel := context.WithCancel(ctx)

	b.agents[clusterID] = &agentConn{
		clusterID: clusterID,
		cmdChan:   cmdChan,
		cancel:    cancel,
	}

	slog.Info("agent connected", "cluster_id", clusterID)

	// Clean up when context done (agent disconnects)
	go func() {
		<-connCtx.Done()
		b.mu.Lock()
		delete(b.agents, clusterID)
		close(cmdChan)
		b.mu.Unlock()
		slog.Info("agent disconnected", "cluster_id", clusterID)
	}()

	return cmdChan, nil
}

// Ack acknowledges successful command processing.
func (b *GRPCCommandBus) Ack(ctx context.Context, commandID string) error {
	b.mu.Lock()
	cmd, ok := b.pending[commandID]
	delete(b.pending, commandID)
	b.mu.Unlock()

	if ok {
		slog.Debug("command acknowledged", "command_id", commandID, "type", cmd.Type)
	}
	return nil
}

// Nack indicates command processing failed.
func (b *GRPCCommandBus) Nack(ctx context.Context, commandID string, retry bool) error {
	b.mu.Lock()
	cmd, ok := b.pending[commandID]
	if !ok {
		b.mu.Unlock()
		return nil
	}

	if !retry || cmd.Attempts >= b.maxRetries {
		delete(b.pending, commandID)
		b.mu.Unlock()
		slog.Warn("command failed permanently", "command_id", commandID, "attempts", cmd.Attempts, "retry", retry)
		// TODO: mark failed in DB, move to dead letter
		return nil
	}

	cmd.Attempts++
	b.mu.Unlock()

	slog.Info("retrying command", "command_id", commandID, "attempt", cmd.Attempts)
	return b.Send(ctx, cmd)
}

// IsConnected returns true if an agent is connected for the cluster.
func (b *GRPCCommandBus) IsConnected(clusterID string) bool {
	b.mu.RLock()
	_, ok := b.agents[clusterID]
	b.mu.RUnlock()
	return ok
}

// Disconnect disconnects an agent.
func (b *GRPCCommandBus) Disconnect(clusterID string) {
	b.mu.Lock()
	if agent, ok := b.agents[clusterID]; ok {
		agent.cancel()
	}
	b.mu.Unlock()
}

// Close shuts down the command bus.
func (b *GRPCCommandBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, agent := range b.agents {
		agent.cancel()
	}
	return nil
}
