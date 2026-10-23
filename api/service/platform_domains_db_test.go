package service

import (
	"maps"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/auth/authtest"
	genDb "github.com/team-loco/loco/api/gen/db"
)

const (
	configuredAppsDomain  = "apps.example.test"
	configuredDemoDomain  = "demo.localhost"
	unlistedPlatformRoot  = "legacy.example.test"
	concurrentDomainSyncs = 8
)

func platformDomainRows(t *testing.T, pool *pgxpool.Pool) map[string]genDb.PlatformDomain {
	t.Helper()
	rows, err := genDb.New(pool).ListPlatformDomains(t.Context(), nil)
	if err != nil {
		t.Fatalf("list platform domains: %v", err)
	}
	out := map[string]genDb.PlatformDomain{}
	for _, row := range rows {
		out[row.Domain] = row
	}
	return out
}

func syncPlatformDomains(t *testing.T, pool *pgxpool.Pool, domains ...string) {
	t.Helper()
	if err := SyncPlatformDomains(t.Context(), pool, domains); err != nil {
		t.Fatalf("sync platform domains: %v", err)
	}
}

func activeDomains(rows map[string]genDb.PlatformDomain) map[string]bool {
	out := map[string]bool{}
	for domain, row := range rows {
		out[domain] = row.IsActive
	}
	return out
}

func TestSyncPlatformDomainsCreatesConfiguredDomains(t *testing.T) {
	pool := authtest.NewPool(t)
	syncPlatformDomains(t, pool, configuredAppsDomain, configuredDemoDomain)

	got := activeDomains(platformDomainRows(t, pool))
	want := map[string]bool{configuredAppsDomain: true, configuredDemoDomain: true}
	if !maps.Equal(got, want) {
		t.Errorf("platform domains = %v, want %v", got, want)
	}
}

func TestSyncPlatformDomainsReactivatesAnInactiveDomain(t *testing.T) {
	pool := authtest.NewPool(t)
	q := genDb.New(pool)
	id, err := q.CreatePlatformDomain(
		t.Context(),
		genDb.CreatePlatformDomainParams{Domain: configuredAppsDomain, IsActive: false},
	)
	if err != nil {
		t.Fatalf("create platform domain: %v", err)
	}

	syncPlatformDomains(t, pool, configuredAppsDomain)

	row := platformDomainRows(t, pool)[configuredAppsDomain]
	if row.ID != id || !row.IsActive {
		t.Errorf("platform domain = %+v, want row %s active", row, id)
	}
}

func TestSyncPlatformDomainsIsIdempotentAcrossRestarts(t *testing.T) {
	pool := authtest.NewPool(t)
	syncPlatformDomains(t, pool, configuredAppsDomain, configuredDemoDomain)
	first := platformDomainRows(t, pool)

	syncPlatformDomains(t, pool, configuredAppsDomain, configuredDemoDomain)

	if second := platformDomainRows(t, pool); !maps.Equal(first, second) {
		t.Errorf("platform domains after restart = %+v, want %+v", second, first)
	}
}

func TestSyncPlatformDomainsLeavesUnlistedDomains(t *testing.T) {
	pool := authtest.NewPool(t)
	syncPlatformDomains(t, pool, configuredAppsDomain, unlistedPlatformRoot)

	syncPlatformDomains(t, pool, configuredDemoDomain)

	got := activeDomains(platformDomainRows(t, pool))
	want := map[string]bool{configuredAppsDomain: true, unlistedPlatformRoot: true, configuredDemoDomain: true}
	if !maps.Equal(got, want) {
		t.Errorf("platform domains = %v, want %v", got, want)
	}
}

func TestSyncPlatformDomainsWithoutDomainsChangesNothing(t *testing.T) {
	pool := authtest.NewPool(t)
	syncPlatformDomains(t, pool, configuredAppsDomain)
	before := platformDomainRows(t, pool)

	syncPlatformDomains(t, pool)

	if after := platformDomainRows(t, pool); !maps.Equal(before, after) {
		t.Errorf("platform domains = %+v, want %+v", after, before)
	}
}

func TestSyncPlatformDomainsConcurrentStartups(t *testing.T) {
	pool := authtest.NewPool(t)
	orders := [][]string{
		{configuredAppsDomain, configuredDemoDomain},
		{configuredDemoDomain, configuredAppsDomain},
	}
	errs := make([]error, concurrentDomainSyncs)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range concurrentDomainSyncs {
		wg.Go(func() {
			<-start
			errs[i] = SyncPlatformDomains(t.Context(), pool, orders[i%len(orders)])
		})
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("startup %d: %v", i, err)
		}
	}
	got := activeDomains(platformDomainRows(t, pool))
	want := map[string]bool{configuredAppsDomain: true, configuredDemoDomain: true}
	if !maps.Equal(got, want) {
		t.Errorf("platform domains = %v, want %v", got, want)
	}
}
