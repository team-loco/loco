package admission

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/db/dbtest"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
)

func TestRequestsAcrossReplicas(t *testing.T) {
	pool := dbtest.NewPool(t)
	other, err := pgxpool.New(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	config := Config{Window: time.Hour, GlobalRequests: 13, PrincipalRequests: 5}
	instances := []*Limiter{New(pool, config), New(other, config)}
	callers := []context.Context{principal(t.Context()), principal(t.Context()), principal(t.Context())}
	var accepted [3]atomic.Int32
	var wg sync.WaitGroup
	for i := range 90 {
		wg.Go(func() {
			caller := i % len(callers)
			next := instances[i%2].Interceptor()(func(context.Context, connect.Spec, connect.ServerStream) error {
				accepted[caller].Add(1)
				return nil
			})
			callErr := next(callers[caller], connect.Spec{}, nil)
			if callErr != nil && connect.CodeOf(callErr) != connect.CodeResourceExhausted {
				t.Errorf("call: %v", callErr)
			}
		})
	}
	wg.Wait()
	var total int32
	for i := range accepted {
		n := accepted[i].Load()
		if n > 5 {
			t.Errorf("principal %d admitted %d", i, n)
		}
		total += n
	}
	if total != 13 {
		t.Fatalf("admitted %d, want 13", total)
	}
	restarted := New(pool, config)
	err = restarted.Interceptor()(func(context.Context, connect.Spec, connect.ServerStream) error {
		t.Error("restart reset the global budget")
		return nil
	})(principal(t.Context()), connect.Spec{}, nil)
	assertExhausted(t, err)
}

func TestPrincipalIsolationAndWindowExpiry(t *testing.T) {
	pool := dbtest.NewPool(t)
	l := New(pool, Config{Window: time.Hour, GlobalRequests: 2, PrincipalRequests: 1})
	next := l.Interceptor()(func(context.Context, connect.Spec, connect.ServerStream) error { return nil })
	neighbor, caller := principal(t.Context()), principal(t.Context())
	if err := next(neighbor, connect.Spec{}, nil); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		assertExhausted(t, next(neighbor, connect.Spec{}, nil))
	}
	if err := next(caller, connect.Spec{}, nil); err != nil {
		t.Fatalf("neighbor consumed global capacity on rejection: %v", err)
	}
	if _, err := pool.Exec(
		t.Context(),
		"UPDATE api_admission SET expires_at = statement_timestamp() - interval '1 second'",
	); err != nil {
		t.Fatal(err)
	}
	if err := next(neighbor, connect.Spec{}, nil); err != nil {
		t.Fatalf("expired budget: %v", err)
	}
	var rows int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM api_admission").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 3 {
		t.Fatalf("expiry grew keys: %d", rows)
	}
}

func TestWorkspaceIsolationAndAtomicWorkBudgets(t *testing.T) {
	pool := dbtest.NewPool(t)
	l := New(pool, Config{Window: time.Hour, WorkspaceRequests: 2, GlobalBuilds: 2, WorkspaceBuilds: 1})
	neighbor, caller := uuid.New(), uuid.New()
	admit := func(id uuid.UUID) error {
		ctx := l.Context(t.Context(), "/loco.build.v1.BuildService/CreateBuild")
		scope := db.EntityScope{EntityType: db.EntityTypeWorkspace, EntityID: id}
		if err := Authorize(ctx, scope); err != nil {
			return err
		}
		return Authorize(ctx, scope)
	}
	if err := admit(neighbor); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		assertExhausted(t, admit(neighbor))
	}
	if err := admit(caller); err != nil {
		t.Fatalf("throttled neighbor affected caller: %v", err)
	}
	var used int64
	key := "requests:workspace:" + neighbor.String()
	if err := pool.QueryRow(t.Context(), "SELECT used FROM api_admission WHERE key = $1", key).
		Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 1 {
		t.Fatalf("rejected work consumed workspace requests: %d", used)
	}
}

func TestStoreOutageAndDisabledBudgets(t *testing.T) {
	pool := dbtest.ClosedPool(t)
	l := New(pool, Config{Window: time.Minute, GlobalRequests: 1})
	next := func(context.Context, connect.Spec, connect.ServerStream) error {
		t.Fatal("admitted during outage")
		return nil
	}
	err := l.Interceptor()(next)(t.Context(), connect.Spec{}, nil)
	assertExhausted(t, err)
	if errors.Unwrap(err) == nil {
		t.Fatal("lost store error")
	}
	disabled := New(pool, Config{Window: time.Minute})
	if err := Authorize(
		disabled.Context(t.Context(), ""),
		db.EntityScope{EntityType: db.EntityTypeResource, EntityID: uuid.New()},
	); err != nil {
		t.Fatalf("disabled admission queried store: %v", err)
	}
	if err := disabled.Interceptor()(
		func(context.Context, connect.Spec, connect.ServerStream) error { return nil },
	)(
		t.Context(),
		connect.Spec{},
		nil,
	); err != nil {
		t.Fatal(err)
	}
}

func principal(ctx context.Context) context.Context {
	return reqctx.WithPrincipal(ctx, reqctx.Principal{Entity: db.Entity{Type: db.EntityTypeUser, ID: uuid.New()}})
}

func assertExhausted(t *testing.T, err error) {
	t.Helper()
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("want resource exhausted, got %v", err)
	}
}
