package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
)

const testSecondDomain = "alt.example.com"

func removeDomain(t *testing.T, f *deployFixture, domain string) error {
	t.Helper()
	ctx := context.Background()
	var domainID uuid.UUID
	lookup := `SELECT id FROM resource_domains WHERE resource_id = $1 AND domain = $2`
	if err := f.pool.QueryRow(ctx, lookup, f.resourceID, domain).Scan(&domainID); err != nil {
		t.Fatalf("find domain %s: %v", domain, err)
	}
	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{LastUsedUpdateInterval: time.Minute})
	t.Cleanup(machine.Close)
	server := NewDomainServer(f.pool, f.queries, machine)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	scoped := context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
	id := domainID.String()
	req := connect.NewRequest(&domainv1.DeleteResourceDomainRequest{DomainId: id})
	_, err := server.DeleteResourceDomain(scoped, req)
	return err
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
