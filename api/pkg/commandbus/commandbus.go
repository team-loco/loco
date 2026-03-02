package commandbus

import (
	"context"
	"fmt"
	"time"
)

// CommandType identifies the type of command.
type CommandType string

const (
	CommandTypeDeploy    CommandType = "deploy"
	CommandTypeDelete    CommandType = "delete"
	CommandTypeScale     CommandType = "scale"
	CommandTypeUpdateEnv CommandType = "update_env"
)

// Command represents a unit of work to be executed by an agent.
type Command struct {
	ID        string
	ClusterID string
	Type      CommandType
	Payload   []byte // JSON-encoded command payload
	CreatedAt time.Time
	Attempts  int
}

// CommandBus dispatches commands to agents and handles acknowledgements.
type CommandBus interface {
	// Send dispatches a command to the target cluster's agent.
	// Returns an error if no agent is connected for the cluster.
	Send(ctx context.Context, cmd *Command) error

	// Receive returns a channel of commands for a cluster.
	// Called by the AgentService when an agent connects.
	Receive(ctx context.Context, clusterID string) (<-chan *Command, error)

	// Ack acknowledges successful command processing.
	Ack(ctx context.Context, commandID string) error

	// Nack indicates command processing failed.
	// If retry is true, the command will be re-sent.
	Nack(ctx context.Context, commandID string, retry bool) error

	// IsConnected returns true if an agent is connected for the cluster.
	IsConnected(clusterID string) bool

	// Close shuts down the command bus.
	Close() error
}

// Config holds command bus configuration.
type Config struct {
	Type       string // "grpc" or "valkey"
	MaxRetries int

	// Valkey-specific (future)
	ValkeyURL string
}

// New creates a CommandBus based on the config type.
func New(cfg *Config) (CommandBus, error) {
	switch cfg.Type {
	case "grpc", "":
		return NewGRPCCommandBus(cfg), nil
	case "valkey":
		return nil, fmt.Errorf("valkey command bus not yet implemented")
	default:
		return nil, fmt.Errorf("unknown command bus type: %s", cfg.Type)
	}
}
