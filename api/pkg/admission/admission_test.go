package admission

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/db/dbtest"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/reqctx"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func requestCaches(t *testing.T) map[string][2]cache.Cache {
	t.Helper()
	backends := make(map[string][2]cache.Cache)
	var memory [2]cache.Cache
	for i := range memory {
		backend, err := cache.NewMemory()
		if err != nil {
			t.Fatal(err)
		}
		memory[i] = backend
		t.Cleanup(func() {
			if err := backend.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	backends["memory"] = memory
	if addr := os.Getenv("LOCO_TEST_VALKEY_URL"); addr != "" {
		var shared [2]cache.Cache
		for i := range shared {
			backend, err := cache.NewValkey(addr)
			if err != nil {
				t.Fatal(err)
			}
			shared[i] = backend
			t.Cleanup(func() {
				if err := backend.Close(); err != nil {
					t.Error(err)
				}
			})
		}
		backends["valkey"] = shared
	}
	return backends
}

func TestRequestRaceWithoutPostgres(t *testing.T) {
	for name, backends := range requestCaches(t) {
		t.Run(name, func(t *testing.T) {
			config := Config{
				Window:            time.Hour,
				GlobalRequests:    13,
				PrincipalRequests: 5,
				AggregateRequests: name == "valkey",
			}
			pool := dbtest.ClosedPool(t)
			instances := []*Limiter{New(pool, backends[0], config), New(pool, backends[1], config)}
			prefix := "{test-admission}:" + uuid.NewString() + ":"
			if config.AggregateRequests {
				for _, l := range instances {
					l.requestPrefix = prefix
				}
			}
			callers := []context.Context{principal(t.Context()), principal(t.Context()), principal(t.Context())}
			var accepted [2][3]atomic.Int32
			var wg sync.WaitGroup
			for i := range 180 {
				wg.Go(func() {
					instance, caller := i%2, i%3
					err := instances[instance].admitRequest(callers[caller], &configv1.GetConfigRequest{})
					if err == nil {
						accepted[instance][caller].Add(1)
						return
					}
					assertExhausted(t, err)
				})
			}
			wg.Wait()
			var total int32
			for instance := range accepted {
				for caller := range accepted[instance] {
					n := accepted[instance][caller].Load()
					if n > 5 {
						t.Errorf("principal exceeded budget: %d", n)
					}
					if config.AggregateRequests && n+accepted[1-instance][caller].Load() > 5 {
						t.Error("principal aggregate exceeded")
					}
					total += n
				}
			}
			want := int32(26)
			if config.AggregateRequests {
				want = 13
			}
			if total != want {
				t.Fatalf("admitted %d, want %d", total, want)
			}
			restarted := New(pool, backends[0], config)
			if config.AggregateRequests {
				restarted.requestPrefix = prefix
				assertExhausted(t, restarted.admitRequest(principal(t.Context()), &configv1.GetConfigRequest{}))
			} else if err := restarted.admitRequest(principal(t.Context()), &configv1.GetConfigRequest{}); err != nil {
				t.Fatalf("replica restart: %v", err)
			}
		})
	}
}

func TestRequestNeighborIsolation(t *testing.T) {
	for name, backends := range requestCaches(t) {
		t.Run(name, func(t *testing.T) {
			l := New(
				dbtest.ClosedPool(t),
				backends[0],
				Config{Window: time.Hour, GlobalRequests: 2, PrincipalRequests: 1},
			)
			neighbor, caller := principal(t.Context()), principal(t.Context())
			if err := l.admitRequest(neighbor, &configv1.GetConfigRequest{}); err != nil {
				t.Fatal(err)
			}
			for range 10 {
				assertExhausted(t, l.admitRequest(neighbor, &configv1.GetConfigRequest{}))
			}
			if err := l.admitRequest(caller, &configv1.GetConfigRequest{}); err != nil {
				t.Fatalf("neighbor consumed global capacity: %v", err)
			}
		})
	}
}

func TestWorkBudgetsAcrossReplicas(t *testing.T) {
	pool := dbtest.NewPool(t)
	config := Config{Window: time.Hour, GlobalBuilds: 3, WorkspaceBuilds: 2}
	instances := []*Limiter{New(pool, nil, config), New(pool, nil, config)}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	workspace := uuid.New()
	for i := range 20 {
		wg.Go(func() {
			err := AdmitWork(instances[i%2].Context(t.Context()), workspace, Build)
			if err == nil {
				accepted.Add(1)
				return
			}
			assertExhausted(t, err)
		})
	}
	wg.Wait()
	if accepted.Load() != 2 {
		t.Fatalf("admitted %d", accepted.Load())
	}
	restarted := New(pool, nil, config)
	assertExhausted(t, AdmitWork(restarted.Context(t.Context()), workspace, Build))
	if err := AdmitWork(restarted.Context(t.Context()), uuid.New(), Build); err != nil {
		t.Fatalf("throttled workspace consumed global capacity: %v", err)
	}
	if _, err := pool.Exec(
		t.Context(),
		"UPDATE api_admission SET expires_at = statement_timestamp() - interval '1 second'",
	); err != nil {
		t.Fatal(err)
	}
	if err := AdmitWork(restarted.Context(t.Context()), workspace, Build); err != nil {
		t.Fatalf("expiry: %v", err)
	}
}

func TestStoreOutageAndDisabledBudgets(t *testing.T) {
	l := New(dbtest.ClosedPool(t), nil, Config{Window: time.Minute, GlobalBuilds: 1})
	err := AdmitWork(l.Context(t.Context()), uuid.New(), Build)
	assertExhausted(t, err)
	if errors.Unwrap(err) == nil {
		t.Fatal("lost store error")
	}
	disabled := New(dbtest.ClosedPool(t), nil, Config{Window: time.Minute})
	if err := AdmitWork(disabled.Context(t.Context()), uuid.New(), Build); err != nil {
		t.Fatal(err)
	}
	if err := disabled.admitRequest(t.Context(), &configv1.GetConfigRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceRequestsUseReadOnlyPostgres(t *testing.T) {
	pool := dbtest.NewPool(t)
	var workspace, resource uuid.UUID
	if err := pool.QueryRow(t.Context(), `
WITH u AS (INSERT INTO users (email) VALUES ('admission@test.local') RETURNING id),
 o AS (INSERT INTO organizations (name,created_by) SELECT 'org',id FROM u RETURNING id),
 w AS (INSERT INTO workspaces (org_id,name,created_by) SELECT o.id,'ws',u.id FROM o,u RETURNING id)
 INSERT INTO resources (workspace_id,name,slug,type,spec,description,spec_version)
 SELECT id,'svc','svc','service','{}','',1 FROM w RETURNING workspace_id,id`).
		Scan(&workspace, &resource); err != nil {
		t.Fatal(err)
	}
	backend, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	// A request UPSERT fails here, while ownership reads remain available.
	if _, err := pool.Exec(
		t.Context(),
		"ALTER DATABASE "+pool.Config().ConnConfig.Database+" SET default_transaction_read_only = on",
	); err != nil {
		t.Fatal(err)
	}
	pool.Reset()
	l := New(pool, backend, Config{Window: time.Hour, WorkspaceRequests: 1})
	ctx := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			Scopes: []db.EntityScope{{EntityType: db.EntityTypeWorkspace, EntityID: workspace, Scope: db.ScopeRead}},
		},
	)
	message := &resourcev1.ListWorkspaceResourcesRequest{WorkspaceId: workspace.String()}
	if err := l.admitRequest(ctx, message); err != nil {
		t.Fatal(err)
	}
	assertExhausted(t, l.admitRequest(ctx, message))

	resourceCtx := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			Scopes: []db.EntityScope{{EntityType: db.EntityTypeResource, EntityID: resource, Scope: db.ScopeRead}},
		},
	)
	assertExhausted(t, l.admitRequest(resourceCtx, message))
	unauthorized := principal(t.Context())
	if err := l.admitRequest(unauthorized, message); err != nil {
		t.Fatalf("unrelated principal spent workspace budget: %v", err)
	}
	var rows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM api_admission").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("request admission wrote %d rows", rows)
	}
}

func principal(ctx context.Context) context.Context {
	return reqctx.WithPrincipal(ctx, reqctx.Principal{Entity: db.Entity{Type: db.EntityTypeUser, ID: uuid.New()}})
}

func assertExhausted(t *testing.T, err error) {
	t.Helper()
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Errorf("want resource exhausted, got %v", err)
	}
}

func TestValkeyFailureClosesRequestAdmission(t *testing.T) {
	addr := os.Getenv("LOCO_TEST_VALKEY_URL")
	if addr == "" {
		t.Skip("LOCO_TEST_VALKEY_URL not set")
	}
	backend, err := cache.NewValkey(addr)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := backend.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	l := New(dbtest.ClosedPool(t), backend, Config{Window: time.Minute, GlobalRequests: 1})
	err = l.admitRequest(t.Context(), &configv1.GetConfigRequest{})
	assertExhausted(t, err)
	if errors.Unwrap(err) == nil {
		t.Fatal("lost cache failure cause")
	}
}
