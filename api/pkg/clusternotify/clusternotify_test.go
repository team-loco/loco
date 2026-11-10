package clusternotify_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/clusternotify"
)

func startNotifier(t *testing.T, pool *pgxpool.Pool) *clusternotify.Notifier {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	poolConfig := pool.Config()
	n := clusternotify.New(poolConfig.ConnConfig, time.Hour)
	if err := n.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	return n
}

func notify(ctx context.Context, db genDb.DBTX, clusterID uuid.UUID) error {
	return genDb.New(db).NotifyClusterPlacements(ctx, clusterID.String())
}

func TestListenerWakesOnlyOnCommittedNotifyForItsCluster(t *testing.T) {
	pool := dbtest.NewPool(t)
	n := startNotifier(t, pool)
	ctx := context.Background()

	clusterID := uuid.New()
	listener := n.Listen(clusterID)
	defer listener.Close()
	other := n.Listen(uuid.New())
	defer other.Close()

	rollback := errors.New("rollback")
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if execErr := notify(ctx, tx, clusterID); execErr != nil {
			return execErr
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("notify in rolled back tx: %v", err)
	}
	select {
	case <-listener.Wake():
		t.Fatal("woken by a rolled back notify")
	case <-time.After(300 * time.Millisecond):
	}

	if err := notify(ctx, pool, clusterID); err != nil {
		t.Fatalf("notify: %v", err)
	}
	select {
	case <-listener.Wake():
	case <-time.After(5 * time.Second):
		t.Fatal("listener not woken by a committed notify")
	}
	select {
	case <-other.Wake():
		t.Fatal("listener for another cluster was woken")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestListenersHoldNoPoolConnections(t *testing.T) {
	pool := dbtest.NewPool(t)
	n := startNotifier(t, pool)
	ctx := context.Background()

	maxConns := int(pool.Config().MaxConns)
	listeners := make([]*clusternotify.Listener, 0, maxConns*2+1)
	for range maxConns * 2 {
		listeners = append(listeners, n.Listen(uuid.New()))
	}
	clusterID := uuid.New()
	target := n.Listen(clusterID)
	listeners = append(listeners, target)

	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("%d pool connections acquired by %d listeners", acquired, len(listeners))
	}
	if err := notify(ctx, pool, clusterID); err != nil {
		t.Fatalf("notify: %v", err)
	}
	select {
	case <-target.Wake():
	case <-time.After(5 * time.Second):
		t.Fatal("listener not woken with the pool's worth of other listeners open")
	}
	for _, l := range listeners {
		l.Close()
	}
}

func TestListenerSurvivesListenConnectionLoss(t *testing.T) {
	pool := dbtest.NewPool(t)
	n := startNotifier(t, pool)
	ctx := context.Background()

	clusterID := uuid.New()
	listener := n.Listen(clusterID)
	defer listener.Close()

	var terminated int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			WHERE query LIKE 'LISTEN %' AND datname = current_database() AND pid <> pg_backend_pid()
		) t`).Scan(&terminated)
	if err != nil || terminated == 0 {
		t.Fatalf("terminate listen connection: %d, %v", terminated, err)
	}

	select {
	case <-listener.Wake():
	case <-time.After(5 * time.Second):
		t.Fatal("listener not woken after the listen connection was replaced")
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		if execErr := notify(ctx, pool, clusterID); execErr != nil {
			t.Fatalf("notify: %v", execErr)
		}
		select {
		case <-listener.Wake():
			return
		case <-time.After(500 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("notifications not delivered after reconnecting")
		}
	}
}

func TestListenerUsesItsOwnConnectionAfterQueryPoolCloses(t *testing.T) {
	pool := dbtest.NewPool(t)
	config := pool.Config().ConnConfig
	notifier := clusternotify.New(config, time.Hour)
	pool.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := notifier.Start(ctx); err != nil {
		t.Fatal(err)
	}
	sender, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sender.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	clusterID := uuid.New()
	target := notifier.Listen(clusterID)
	defer target.Close()
	if err := notify(ctx, sender, clusterID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-target.Wake():
	case <-time.After(time.Second):
		t.Fatal("direct listener did not receive the notification")
	}
}
