package clusternotify_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/pkg/clusternotify"
)

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	baseURL := os.Getenv("LOCO_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("LOCO_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := admin.Close(context.Background()); closeErr != nil {
			t.Logf("close admin connection: %v", closeErr)
		}
	})

	dbName := "clusternotify_" + uuid.NewString()[:8]
	ident := pgx.Identifier{dbName}.Sanitize()
	if _, createErr := admin.Exec(ctx, "CREATE DATABASE "+ident); createErr != nil {
		t.Fatalf("create database: %v", createErr)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	parsed.Path = "/" + dbName
	dbURL := parsed.String()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, dropErr := admin.Exec(context.Background(), "DROP DATABASE "+ident+" WITH (FORCE)"); dropErr != nil {
			t.Logf("drop database: %v", dropErr)
		}
	})
	return pool
}

func startNotifier(t *testing.T, pool *pgxpool.Pool) *clusternotify.Notifier {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	n := clusternotify.New(pool, time.Hour)
	if err := n.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	return n
}

const notifySQL = "SELECT pg_notify($1, $2)"

func TestListenerWakesOnlyOnCommittedNotifyForItsCluster(t *testing.T) {
	pool := newPool(t)
	n := startNotifier(t, pool)
	ctx := context.Background()

	clusterID := uuid.New()
	listener := n.Listen(ctx, clusterID)
	defer listener.Close()
	other := n.Listen(ctx, uuid.New())
	defer other.Close()

	rollback := errors.New("rollback")
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, execErr := tx.Exec(ctx, notifySQL, clusternotify.Channel, clusterID.String()); execErr != nil {
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

	if _, err := pool.Exec(ctx, notifySQL, clusternotify.Channel, clusterID.String()); err != nil {
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
	pool := newPool(t)
	n := startNotifier(t, pool)
	ctx := context.Background()

	maxConns := int(pool.Config().MaxConns)
	listeners := make([]*clusternotify.Listener, 0, maxConns*2+1)
	for range maxConns * 2 {
		listeners = append(listeners, n.Listen(ctx, uuid.New()))
	}
	clusterID := uuid.New()
	target := n.Listen(ctx, clusterID)
	listeners = append(listeners, target)

	if acquired := pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("%d pool connections acquired by %d listeners", acquired, len(listeners))
	}
	if _, err := pool.Exec(ctx, notifySQL, clusternotify.Channel, clusterID.String()); err != nil {
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
	pool := newPool(t)
	n := startNotifier(t, pool)
	ctx := context.Background()

	clusterID := uuid.New()
	listener := n.Listen(ctx, clusterID)
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
		if _, execErr := pool.Exec(ctx, notifySQL, clusternotify.Channel, clusterID.String()); execErr != nil {
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
