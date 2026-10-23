package service

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
)

const (
	testSecondDomain   = "alt.example.com"
	testDomainPlatform = "example.com"
	domainRaceRounds   = 25
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
	server := NewDomainServer(f.pool, f.queries, machine, testServiceDefaults(), testClusterSilentAfter)
	f.serveDomains(t, testDomainPlatform)
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
	req := &domainv1.DeleteResourceDomainRequest{DomainId: id}
	_, err := c.server.DeleteResourceDomain(c.ctx, req)
	return err
}

func (c *domainClient) add(domain string) error {
	label := strings.TrimSuffix(domain, "."+testDomainPlatform)
	return c.addUnder(label, testDomainPlatform)
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

func TestRemovingTheOnlyDomainMakesTheRunningDeploymentPrivate(t *testing.T) {
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

type domainEventRow struct {
	eventType string
	subjectID string
}

func domainEvents(t *testing.T, f *deployFixture, domain string) []domainEventRow {
	t.Helper()
	query := `SELECT type, subject_id FROM events WHERE subject_type = $1 AND data->>$2 = $3 ORDER BY seq`
	rows, err := f.pool.Query(context.Background(), query, events.SubjectDomain, events.FieldDomain, domain)
	if err != nil {
		t.Fatalf("list domain events: %v", err)
	}
	defer rows.Close()
	var out []domainEventRow
	for rows.Next() {
		var row domainEventRow
		var subjectID uuid.UUID
		if scanErr := rows.Scan(&row.eventType, &subjectID); scanErr != nil {
			t.Fatalf("scan domain event: %v", scanErr)
		}
		row.subjectID = subjectID.String()
		out = append(out, row)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read domain events: %v", rowsErr)
	}
	return out
}

func TestAddingAndRemovingTheLastDomainRecordEvents(t *testing.T) {
	f := newDeployFixture(t)
	client := newDomainClient(t, f)

	if err := client.add(testPrimaryDomain); err != nil {
		t.Fatalf("add: %v", err)
	}
	id := client.domainID(t, testPrimaryDomain)
	if err := client.removeByID(id); err != nil {
		t.Fatalf("remove the last domain: %v", err)
	}

	got := domainEvents(t, f, testPrimaryDomain)
	want := []domainEventRow{
		{eventType: events.DomainCreated, subjectID: id},
		{eventType: events.DomainDeleted, subjectID: id},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("domain events = %+v, want %+v", got, want)
	}
}

func TestRejectedPrimaryDomainRemovalRecordsNoEvent(t *testing.T) {
	f := newDeployFixture(t)
	f.addDomain(t, testPrimaryDomain, true)
	f.addDomain(t, testSecondDomain, false)

	err := removeDomain(t, f, testPrimaryDomain)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if got := domainEvents(t, f, testPrimaryDomain); len(got) != 0 {
		t.Fatalf("domain events after a rejected removal = %+v, want none", got)
	}
}

func TestPrimaryDomainChangesUpdateRunningPlacement(t *testing.T) {
	f := newDeployFixture(t)
	f.addDomain(t, testPrimaryDomain, true)
	f.addDomain(t, testSecondDomain, false)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	client := newDomainClient(t, f)
	before := f.placement(t, f.clusterID)
	request := &domainv1.SetPrimaryResourceDomainRequest{
		ResourceId: f.resourceID.String(),
		DomainId:   client.domainID(t, testSecondDomain),
	}
	if _, err := client.server.SetPrimaryResourceDomain(client.ctx, request); err != nil {
		t.Fatal(err)
	}
	after := f.placement(t, f.clusterID)
	if after.DesiredRevision != before.DesiredRevision+1 {
		t.Fatalf("revision = %d, want %d", after.DesiredRevision, before.DesiredRevision+1)
	}
	if got := desiredPayload(t, f).AppSpec.ServiceSpec.Routing.HostName; got != testSecondDomain {
		t.Fatalf("hostname = %s, want %s", got, testSecondDomain)
	}
	if after.DeploymentID == nil || before.DeploymentID == nil || *after.DeploymentID != *before.DeploymentID {
		t.Fatal("domain change replaced the deployment")
	}
}

func TestDomainRenameNotifiesTheAgentAndPreservesPlacementSettings(t *testing.T) {
	f := newDeployFixture(t)
	client := newDomainClient(t, f)
	if err := client.add(testPrimaryDomain); err != nil {
		t.Fatal(err)
	}
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	before := desiredPayload(t, f)
	listener, err := f.pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, listenErr := listener.Exec(context.Background(), "LISTEN placements"); listenErr != nil {
		t.Fatal(listenErr)
	}
	defer func() {
		if _, unlistenErr := listener.Exec(context.Background(), "UNLISTEN placements"); unlistenErr != nil {
			t.Error(unlistenErr)
		}
	}()
	request := &domainv1.UpdateResourceDomainRequest{
		DomainId: client.domainID(t, testPrimaryDomain),
		Domain:   new(testSecondDomain),
	}
	if _, updateErr := client.server.UpdateResourceDomain(client.ctx, request); updateErr != nil {
		t.Fatal(updateErr)
	}
	after := desiredPayload(t, f)
	if after.AppSpec.ServiceSpec.Routing.HostName != testSecondDomain {
		t.Fatalf("route = %+v", after.AppSpec.ServiceSpec.Routing)
	}
	before.AppSpec.ServiceSpec.Routing.HostName = testSecondDomain
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rename changed placement fields other than its hostname")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	notification, err := listener.Conn().WaitForNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if notification.Payload != f.clusterID.String() {
		t.Fatalf("notification = %s", notification.Payload)
	}
}

func TestAddingTheFirstDomainUpdatesAPrivatePlacement(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	client := newDomainClient(t, f)
	if err := client.add(testPrimaryDomain); err != nil {
		t.Fatal(err)
	}
	route := desiredPayload(t, f).AppSpec.ServiceSpec.Routing
	if route == nil || route.HostName != testPrimaryDomain || route.PathPrefix != "/" ||
		route.IdleTimeout != testIdleTimeout {
		t.Fatalf("route = %+v", route)
	}
	revision := f.placement(t, f.clusterID).DesiredRevision
	if err := client.add(testSecondDomain); err != nil {
		t.Fatal(err)
	}
	if got := f.placement(t, f.clusterID).DesiredRevision; got != revision {
		t.Fatalf("non-primary domain advanced revision to %d from %d", got, revision)
	}
}

func TestDomainChangeRollsBackWhenPlacementCannotBeUpdated(t *testing.T) {
	f := newDeployFixture(t)
	client := newDomainClient(t, f)
	if err := client.add(testPrimaryDomain); err != nil {
		t.Fatal(err)
	}
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		context.Background(),
		`UPDATE placements SET desired_spec = '{}' WHERE resource_id = $1`,
		f.resourceID,
	); err != nil {
		t.Fatal(err)
	}
	domainID := client.domainID(t, testPrimaryDomain)
	request := &domainv1.UpdateResourceDomainRequest{DomainId: domainID, Domain: new(testSecondDomain)}
	_, err := client.server.UpdateResourceDomain(client.ctx, request)
	wantCode(t, err, connect.CodeInternal)
	if got := client.domainID(t, testPrimaryDomain); got != domainID {
		t.Fatal("failed placement update changed the domain")
	}
	if got := f.placement(t, f.clusterID).DesiredRevision; got != 1 {
		t.Fatalf("revision = %d, want 1", got)
	}
}
