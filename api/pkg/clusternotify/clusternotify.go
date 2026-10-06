package clusternotify

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	Channel             = "placements"
	DefaultPollInterval = 5 * time.Second
	reconnectDelay      = time.Second
)

type Notifier struct {
	pool         *pgxpool.Pool
	pollInterval time.Duration

	mu          sync.Mutex
	subscribers map[uuid.UUID]map[*Listener]struct{}
}

func New(pool *pgxpool.Pool, pollInterval time.Duration) *Notifier {
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}
	return &Notifier{
		pool:         pool,
		pollInterval: pollInterval,
		subscribers:  make(map[uuid.UUID]map[*Listener]struct{}),
	}
}

type Listener struct {
	notifier  *Notifier
	clusterID uuid.UUID
	wake      chan struct{}
	cancel    context.CancelFunc
	done      chan struct{}
}

func (n *Notifier) Start(ctx context.Context) error {
	conn, err := n.listen(ctx)
	if err != nil {
		return err
	}
	go n.run(ctx, conn)
	return nil
}

func (n *Notifier) listen(ctx context.Context) (*pgx.Conn, error) {
	connConfig := n.pool.Config().ConnConfig.Copy()
	conn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		return nil, fmt.Errorf("connect listen connection: %w", err)
	}
	channel := pgx.Identifier{Channel}.Sanitize()
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		closeConn(ctx, conn)
		return nil, fmt.Errorf("listen on %s: %w", channel, err)
	}
	return conn, nil
}

func (n *Notifier) run(ctx context.Context, conn *pgx.Conn) {
	for {
		err := n.dispatch(ctx, conn)
		closeConn(ctx, conn)
		if ctx.Err() != nil {
			return
		}
		slog.ErrorContext(ctx, "placement listener disconnected", "error", err)
		conn = n.reconnect(ctx)
		if conn == nil {
			return
		}
		n.wakeAll()
	}
}

func (n *Notifier) dispatch(ctx context.Context, conn *pgx.Conn) error {
	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("wait for notification: %w", err)
		}
		clusterID, parseErr := uuid.Parse(notification.Payload)
		if parseErr != nil {
			slog.WarnContext(ctx, "ignoring placement notification", "payload", notification.Payload)
			continue
		}
		n.wakeCluster(clusterID)
	}
}

func (n *Notifier) reconnect(ctx context.Context) *pgx.Conn {
	ticker := time.NewTicker(reconnectDelay)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		conn, err := n.listen(ctx)
		if err == nil {
			return conn
		}
		slog.ErrorContext(ctx, "failed to reconnect placement listener", "error", err)
	}
}

func (n *Notifier) wakeCluster(clusterID uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for l := range n.subscribers[clusterID] {
		l.signal()
	}
}

func (n *Notifier) wakeAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, listeners := range n.subscribers {
		for l := range listeners {
			l.signal()
		}
	}
}

func (n *Notifier) Listen(ctx context.Context, clusterID uuid.UUID) *Listener {
	pollCtx, cancel := context.WithCancel(ctx)
	l := &Listener{
		notifier:  n,
		clusterID: clusterID,
		wake:      make(chan struct{}, 1),
		cancel:    cancel,
		done:      make(chan struct{}),
	}

	n.mu.Lock()
	listeners, ok := n.subscribers[clusterID]
	if !ok {
		listeners = make(map[*Listener]struct{})
		n.subscribers[clusterID] = listeners
	}
	listeners[l] = struct{}{}
	n.mu.Unlock()

	go l.poll(pollCtx, n.pollInterval)
	return l
}

func (l *Listener) Wake() <-chan struct{} {
	return l.wake
}

func (l *Listener) Close() {
	l.cancel()
	<-l.done

	l.notifier.mu.Lock()
	defer l.notifier.mu.Unlock()
	listeners := l.notifier.subscribers[l.clusterID]
	delete(listeners, l)
	if len(listeners) == 0 {
		delete(l.notifier.subscribers, l.clusterID)
	}
}

func (l *Listener) signal() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *Listener) poll(ctx context.Context, interval time.Duration) {
	defer close(l.done)
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
