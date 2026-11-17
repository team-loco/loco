package pgnotify

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const reconnectDelay = time.Second

type Notifier struct {
	channel      string
	connConfig   *pgx.ConnConfig
	pollInterval time.Duration
	conn         *pgx.Conn

	mu          sync.Mutex
	subscribers map[uuid.UUID]map[*Listener]struct{}
}

func New(connConfig *pgx.ConnConfig, channel string, pollInterval time.Duration) *Notifier {
	listenConfig := connConfig.Copy()
	return &Notifier{
		channel:      channel,
		connConfig:   listenConfig,
		pollInterval: pollInterval,
		subscribers:  make(map[uuid.UUID]map[*Listener]struct{}),
	}
}

type Listener struct {
	notifier *Notifier
	key      uuid.UUID
	wake     chan struct{}
}

// Connect opens the LISTEN connection that Run dispatches from, failing if it cannot.
func (n *Notifier) Connect(ctx context.Context) error {
	conn, err := n.listen(ctx)
	if err != nil {
		return err
	}
	n.conn = conn
	return nil
}

// Run dispatches notifications on the connection from Connect until ctx is canceled,
// and returns only after the connection is closed.
func (n *Notifier) Run(ctx context.Context) {
	n.run(ctx, n.conn)
}

func (n *Notifier) listen(ctx context.Context) (*pgx.Conn, error) {
	connConfig := n.connConfig.Copy()
	conn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		return nil, fmt.Errorf("connect listen connection: %w", err)
	}
	identifier := pgx.Identifier{n.channel}.Sanitize()
	if _, err := conn.Exec(ctx, "LISTEN "+identifier); err != nil {
		closeConn(ctx, conn)
		return nil, fmt.Errorf("listen on %s: %w", identifier, err)
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
		slog.ErrorContext(ctx, "notification listener disconnected", "error", err)
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
		key, parseErr := uuid.Parse(notification.Payload)
		if parseErr != nil {
			slog.WarnContext(ctx, "ignoring notification", "payload", notification.Payload)
			continue
		}
		n.wakeKey(key)
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
		slog.ErrorContext(ctx, "failed to reconnect notification listener", "error", err)
	}
}

func (n *Notifier) wakeKey(key uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for l := range n.subscribers[key] {
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

// PollInterval is how often a listener's owner should re-check for changes in case a notification was missed.
func (n *Notifier) PollInterval() time.Duration {
	return n.pollInterval
}

func (n *Notifier) Listen(key uuid.UUID) *Listener {
	l := &Listener{
		notifier: n,
		key:      key,
		wake:     make(chan struct{}, 1),
	}

	n.mu.Lock()
	listeners, ok := n.subscribers[key]
	if !ok {
		listeners = make(map[*Listener]struct{})
		n.subscribers[key] = listeners
	}
	listeners[l] = struct{}{}
	n.mu.Unlock()

	return l
}

func (l *Listener) Wake() <-chan struct{} {
	return l.wake
}

func (l *Listener) Close() {
	l.notifier.mu.Lock()
	defer l.notifier.mu.Unlock()
	listeners := l.notifier.subscribers[l.key]
	delete(listeners, l)
	if len(listeners) == 0 {
		delete(l.notifier.subscribers, l.key)
	}
}

func (l *Listener) signal() {
	select {
	case l.wake <- struct{}{}:
	default:
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
