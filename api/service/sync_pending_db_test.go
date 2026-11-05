package service

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

type pendingSpecTracer struct{ loads int }

func (tracer *pendingSpecTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	if strings.Contains(data.SQL, "id = ANY") {
		tracer.loads++
	}
	return ctx
}

func (*pendingSpecTracer) TraceQueryEnd(_ context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {}

func TestPendingPollLoadsOnlyUnsentSpecifications(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatal(err)
	}
	tracer := &pendingSpecTracer{}
	config := f.pool.Config()
	config.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	server := f.agentServer()
	server.db = pool
	var sends int
	session := newSyncSession(server, f.clusterID, func(_ *agentv1.SyncResponse) error { sends++; return nil })
	if err := session.sendPendingPlacements(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.sendPendingPlacements(ctx); err != nil {
		t.Fatal(err)
	}
	if sends != 1 || tracer.loads != 1 {
		t.Fatalf("sends = %d, spec loads = %d, want 1 each", sends, tracer.loads)
	}
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatal(err)
	}
	if err := session.sendPendingPlacements(ctx); err != nil {
		t.Fatal(err)
	}
	if sends != 2 || tracer.loads != 2 {
		t.Fatalf("sends = %d, spec loads = %d, want 2 each", sends, tracer.loads)
	}
}

func TestSyncSessionEndsWhenAReadFails(t *testing.T) {
	f := newBuildFixture(t)
	ctx := context.Background()
	closed, err := pgxpool.NewWithConfig(ctx, f.pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	reads := map[string]func(*syncSession) error{
		"pending placements": func(ss *syncSession) error { return ss.sendPendingPlacements(ctx) },
		"queued builds":      func(ss *syncSession) error { return ss.startQueuedBuilds(ctx) },
		"held builds": func(ss *syncSession) error {
			ss.builds.active[uuid.New()] = struct{}{}
			return ss.cancelStaleBuilds(ctx)
		},
		"inventory": func(ss *syncSession) error { return ss.reconcileInventory(ctx, &agentv1.Inventory{}) },
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			session, _ := f.dispatchSession()
			session.server.db = closed
			session.server.queries = genDb.New(closed)
			if err := read(session); err == nil {
				t.Fatal("read failure returned nil, want an error that ends the stream")
			}
		})
	}
}
