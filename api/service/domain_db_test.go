package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
)

const (
	testSecondDomain = "alt.example.com"
	domainRaceRounds = 25
)

type domainClient struct {
	server *DomainServer
	ctx    context.Context
	f      *deployFixture
}

func newDomainClient(t *testing.T, f *deployFixture) *domainClient {
	t.Helper()
	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{LastUsedUpdateInterval: time.Minute})
	t.Cleanup(machine.Close)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	scoped := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	server := NewDomainServer(f.pool, f.queries, machine)
	return &domainClient{server: server, ctx: scoped, f: f}
}

func (c *domainClient) domainID(t *testing.T, domain string) string {
	t.Helper()
	var id uuid.UUID
	lookup := `SELECT id FROM resource_domains WHERE resource_id = $1 AND domain = $2`
	if err := c.f.pool.QueryRow(context.Background(), lookup, c.f.resourceID, domain).Scan(&id); err != nil {
		t.Fatalf("find domain %s: %v", domain, err)
	}
	return id.String()
}

func (c *domainClient) removeByID(id string) error {
	req := connect.NewRequest(&domainv1.DeleteResourceDomainRequest{DomainId: id})
	_, err := c.server.DeleteResourceDomain(c.ctx, req)
	return err
}

func (c *domainClient) add(domain string) error {
	input := &domainv1.DomainInput{DomainSource: domainv1.DomainType_DOMAIN_TYPE_USER_PROVIDED, Domain: &domain}
	resourceID := c.f.resourceID.String()
	req := connect.NewRequest(&domainv1.CreateResourceDomainRequest{ResourceId: resourceID, Domain: input})
	_, err := c.server.CreateResourceDomain(c.ctx, req)
	return err
}

func removeDomain(t *testing.T, f *deployFixture, domain string) error {
	t.Helper()
	client := newDomainClient(t, f)
	id := client.domainID(t, domain)
	return client.removeByID(id)
}

func TestConcurrentDomainAddAndRemoveKeepOnePrimary(t *testing.T) {
	f := newDeployFixture(t)
	client := newDomainClient(t, f)
	for round := range domainRaceRounds {
		removed := fmt.Sprintf("old-%d.example.com", round)
		added := fmt.Sprintf("new-%d.example.com", round)
		f.addDomain(t, removed, true)
		removedID := client.domainID(t, removed)

		var wg sync.WaitGroup
		var removeErr, addErr error
		wg.Go(func() { removeErr = client.removeByID(removedID) })
		wg.Go(func() { addErr = client.add(added) })
		wg.Wait()

		if addErr != nil {
			t.Fatalf("round %d: add %s: %v", round, added, addErr)
		}
		if removeErr != nil && connect.CodeOf(removeErr) != connect.CodeFailedPrecondition {
			t.Fatalf("round %d: remove %s: %v", round, removed, removeErr)
		}
		total := f.count(t, `SELECT count(*) FROM resource_domains WHERE resource_id = $1`)
		primaries := f.count(t, `SELECT count(*) FROM resource_domains WHERE resource_id = $1 AND is_primary`)
		if total > 0 && primaries != 1 {
			t.Fatalf("round %d: %d domains with %d primaries, want exactly 1 primary", round, total, primaries)
		}
		reset := `DELETE FROM resource_domains WHERE resource_id = $1`
		if _, err := f.pool.Exec(context.Background(), reset, f.resourceID); err != nil {
			t.Fatalf("round %d: reset domains: %v", round, err)
		}
	}
}

func TestAddingADomainToAResourceWithoutAPrimaryMakesItPrimary(t *testing.T) {
	f := newDeployFixture(t)
	f.addDomain(t, testSecondDomain, false)
	client := newDomainClient(t, f)

	if err := client.add(testPrimaryDomain); err != nil {
		t.Fatalf("add: %v", err)
	}
	primaries := f.count(t, `SELECT count(*) FROM resource_domains WHERE resource_id = $1 AND is_primary`)
	if primaries != 1 {
		t.Fatalf("%d primaries, want 1", primaries)
	}
	var primary string
	query := `SELECT domain FROM resource_domains WHERE resource_id = $1 AND is_primary`
	if err := f.pool.QueryRow(context.Background(), query, f.resourceID).Scan(&primary); err != nil {
		t.Fatalf("primary: %v", err)
	}
	if primary != testPrimaryDomain {
		t.Fatalf("primary = %s, want the added %s", primary, testPrimaryDomain)
	}
}

func TestRemovingTheOnlyDomainMakesTheNextDeploymentPrivate(t *testing.T) {
	f := newDeployFixture(t)
	f.addDomain(t, testPrimaryDomain, true)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if routing := desiredPayload(t, f).AppSpec.ServiceSpec.Routing; routing == nil {
		t.Fatal("routing is nil before the domain is removed")
	}

	if err := removeDomain(t, f, testPrimaryDomain); err != nil {
		t.Fatalf("remove the only domain: %v", err)
	}
	if n := f.count(t, `SELECT count(*) FROM resource_domains WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d domains left, want 0", n)
	}
	if err := scaleResource(t, f, 2); err != nil {
		t.Fatalf("scale: %v", err)
	}
	if routing := desiredPayload(t, f).AppSpec.ServiceSpec.Routing; routing != nil {
		t.Fatalf("routing = %+v after the domain was removed, want none", routing)
	}
}

func TestRemovingThePrimaryDomainRequiresAnotherPrimaryFirst(t *testing.T) {
	f := newDeployFixture(t)
	f.addDomain(t, testPrimaryDomain, true)
	f.addDomain(t, testSecondDomain, false)

	err := removeDomain(t, f, testPrimaryDomain)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if n := f.count(t, `SELECT count(*) FROM resource_domains WHERE resource_id = $1`); n != 2 {
		t.Fatalf("%d domains after a rejected removal, want 2", n)
	}

	if err := removeDomain(t, f, testSecondDomain); err != nil {
		t.Fatalf("remove the non-primary domain: %v", err)
	}
	if err := removeDomain(t, f, testPrimaryDomain); err != nil {
		t.Fatalf("remove the primary once it is the only domain: %v", err)
	}
}
