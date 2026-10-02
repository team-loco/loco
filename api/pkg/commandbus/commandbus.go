package commandbus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
)

type CommandType string

const (
	CommandTypeDeploy CommandType = "deploy"
	CommandTypeDelete CommandType = "delete"
)

const (
	DefaultMaxAttempts  = 5
	DefaultLease        = 2 * time.Minute
	DefaultPollInterval = 5 * time.Second
	DefaultBatchSize    = 10
	baseBackoff         = 5 * time.Second
	maxBackoff          = 5 * time.Minute
	channelPrefix       = "agent_commands_"
)

type Command struct {
	ID           uuid.UUID
	ClusterID    uuid.UUID
	ResourceID   uuid.UUID
	DeploymentID *uuid.UUID
	Type         CommandType
	Payload      []byte
	CreatedAt    time.Time
	Attempts     int32
}

type NewCommand struct {
	ClusterID    uuid.UUID
	ResourceID   uuid.UUID
	DeploymentID *uuid.UUID
	Type         CommandType
	Payload      []byte
}

type Config struct {
	Lease        time.Duration
	PollInterval time.Duration
	BatchSize    int32
}

type Bus struct {
	pool    *pgxpool.Pool
	queries genDb.Querier
	cfg     Config
}

func New(pool *pgxpool.Pool, queries genDb.Querier, cfg Config) *Bus {
	if cfg.Lease <= 0 {
		cfg.Lease = DefaultLease
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	return &Bus{pool: pool, queries: queries, cfg: cfg}
}

func ChannelName(clusterID uuid.UUID) string {
	return channelPrefix + clusterID.String()
}

func Backoff(attempts int32) time.Duration {
	if attempts < 1 {
		return baseBackoff
	}
	exp := math.Pow(2, float64(attempts-1))
	delay := time.Duration(float64(baseBackoff) * exp)
	if delay <= 0 || delay > maxBackoff {
		return maxBackoff
	}
	return delay
}

func Enqueue(ctx context.Context, q genDb.Querier, cmd NewCommand) (uuid.UUID, error) {
	superseded, err := q.SupersedeAgentCommands(ctx, genDb.SupersedeAgentCommandsParams{
		ResourceID: cmd.ResourceID,
		ClusterID:  cmd.ClusterID,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("supersede commands: %w", err)
	}
	if superseded > 0 {
		slog.InfoContext(ctx, "superseded older agent commands",
			"resource_id", cmd.ResourceID,
			"cluster_id", cmd.ClusterID,
			"count", superseded,
		)
	}

	id, err := q.InsertAgentCommand(ctx, genDb.InsertAgentCommandParams{
		ClusterID:    cmd.ClusterID,
		ResourceID:   cmd.ResourceID,
		DeploymentID: cmd.DeploymentID,
		Type:         genDb.AgentCommandType(cmd.Type),
		Payload:      cmd.Payload,
		MaxAttempts:  DefaultMaxAttempts,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert command: %w", err)
	}

	channel := ChannelName(cmd.ClusterID)
	if err := q.NotifyAgentCommands(ctx, channel); err != nil {
		return uuid.Nil, fmt.Errorf("notify agent commands: %w", err)
	}

	return id, nil
}

func (b *Bus) Claim(ctx context.Context, clusterID uuid.UUID) ([]Command, error) {
	if err := b.expire(ctx, clusterID); err != nil {
		return nil, err
	}

	rows, err := b.queries.ClaimAgentCommands(ctx, genDb.ClaimAgentCommandsParams{
		LeaseSeconds: b.cfg.Lease.Seconds(),
		ClusterID:    clusterID,
		BatchSize:    b.cfg.BatchSize,
	})
	if err != nil {
		return nil, fmt.Errorf("claim commands: %w", err)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID.String() < rows[j].ID.String()
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})

	cmds := make([]Command, 0, len(rows))
	for _, row := range rows {
		cmds = append(cmds, Command{
			ID:           row.ID,
			ClusterID:    row.ClusterID,
			ResourceID:   row.ResourceID,
			DeploymentID: row.DeploymentID,
			Type:         CommandType(row.Type),
			Payload:      row.Payload,
			CreatedAt:    row.CreatedAt,
			Attempts:     row.Attempts,
		})
	}
	return cmds, nil
}

func (b *Bus) expire(ctx context.Context, clusterID uuid.UUID) error {
	return pgx.BeginFunc(ctx, b.pool, func(tx pgx.Tx) error {
		qtx := genDb.New(tx)
		expired, err := qtx.ExpireAgentCommands(ctx, clusterID)
		if err != nil {
			return fmt.Errorf("expire commands: %w", err)
		}
		for _, row := range expired {
			slog.WarnContext(ctx, "agent command failed after its final lease expired",
				"command_id", row.ID,
				"cluster_id", clusterID,
			)
			if row.Type != genDb.AgentCommandTypeDeploy || row.DeploymentID == nil {
				continue
			}
			message := ""
			if row.LastError != nil {
				message = *row.LastError
			}
			if err := qtx.FailDeployment(ctx, genDb.FailDeploymentParams{
				ID:      *row.DeploymentID,
				Message: message,
			}); err != nil {
				return fmt.Errorf("fail deployment %s: %w", row.DeploymentID, err)
			}
		}
		return nil
	})
}

func (b *Bus) Ack(ctx context.Context, clusterID, commandID uuid.UUID) error {
	n, err := b.queries.CompleteAgentCommand(ctx, genDb.CompleteAgentCommandParams{
		ID:        commandID,
		ClusterID: clusterID,
	})
	if err != nil {
		return fmt.Errorf("complete command: %w", err)
	}
	if n == 0 {
		slog.WarnContext(ctx, "ignoring ack for a command not delivered to this cluster",
			"command_id", commandID,
			"cluster_id", clusterID,
		)
	}
	return nil
}

func (b *Bus) Nack(ctx context.Context, clusterID, commandID uuid.UUID, retry bool, errMessage string) error {
	return pgx.BeginFunc(ctx, b.pool, func(tx pgx.Tx) error {
		qtx := genDb.New(tx)
		cmd, err := qtx.GetDeliveredAgentCommandForUpdate(ctx, genDb.GetDeliveredAgentCommandForUpdateParams{
			ID:        commandID,
			ClusterID: clusterID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			slog.WarnContext(ctx, "ignoring nack for a command not delivered to this cluster",
				"command_id", commandID,
				"cluster_id", clusterID,
			)
			return nil
		}
		if err != nil {
			return fmt.Errorf("get command: %w", err)
		}

		if retry && cmd.Attempts < cmd.MaxAttempts {
			backoff := Backoff(cmd.Attempts)
			if err := qtx.RetryAgentCommand(ctx, genDb.RetryAgentCommandParams{
				ID:             cmd.ID,
				LastError:      &errMessage,
				BackoffSeconds: backoff.Seconds(),
			}); err != nil {
				return fmt.Errorf("retry command: %w", err)
			}
			slog.InfoContext(ctx, "agent command will be retried",
				"command_id", cmd.ID,
				"attempts", cmd.Attempts,
				"backoff", backoff,
			)
			return nil
		}

		if err := qtx.FailAgentCommand(ctx, genDb.FailAgentCommandParams{
			ID:        cmd.ID,
			LastError: &errMessage,
		}); err != nil {
			return fmt.Errorf("fail command: %w", err)
		}
		slog.WarnContext(ctx, "agent command failed permanently",
			"command_id", cmd.ID,
			"attempts", cmd.Attempts,
			"error", errMessage,
		)

		if cmd.Type != genDb.AgentCommandTypeDeploy || cmd.DeploymentID == nil {
			return nil
		}
		if err := qtx.FailDeployment(ctx, genDb.FailDeploymentParams{
			ID:      *cmd.DeploymentID,
			Message: errMessage,
		}); err != nil {
			return fmt.Errorf("fail deployment %s: %w", cmd.DeploymentID, err)
		}
		return nil
	})
}

type Listener struct {
	conn   *pgxpool.Conn
	cancel context.CancelFunc
	wake   chan struct{}
	errs   chan error
	done   chan struct{}
}

func (b *Bus) Listen(ctx context.Context, clusterID uuid.UUID) (*Listener, error) {
	conn, err := b.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire listen connection: %w", err)
	}

	channelName := ChannelName(clusterID)
	channel := pgx.Identifier{channelName}.Sanitize()
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		hijacked := conn.Hijack()
		closeConn(ctx, hijacked)
		return nil, fmt.Errorf("listen on %s: %w", channel, err)
	}

	listenCtx, cancel := context.WithCancel(ctx)
	l := &Listener{
		conn:   conn,
		cancel: cancel,
		wake:   make(chan struct{}, 1),
		errs:   make(chan error, 1),
		done:   make(chan struct{}),
	}
	go l.waitForNotifications(listenCtx)
	go l.poll(listenCtx, b.cfg.PollInterval)
	return l, nil
}

func (l *Listener) Wake() <-chan struct{} {
	return l.wake
}

func (l *Listener) Err() <-chan error {
	return l.errs
}

func (l *Listener) Close() {
	l.cancel()
	<-l.done
	hijacked := l.conn.Hijack()
	closeConn(context.Background(), hijacked)
}

func (l *Listener) signal() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *Listener) waitForNotifications(ctx context.Context) {
	defer close(l.done)
	for {
		_, err := l.conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			l.errs <- fmt.Errorf("wait for notification: %w", err)
			return
		}
		l.signal()
	}
}

func (l *Listener) poll(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.signal()
		}
	}
}

func closeConn(ctx context.Context, conn *pgx.Conn) {
	detached := context.WithoutCancel(ctx)
	closeCtx, cancel := context.WithTimeout(detached, 5*time.Second)
	defer cancel()
	if err := conn.Close(closeCtx); err != nil {
		slog.WarnContext(ctx, "failed to close listen connection", "error", err)
	}
}
