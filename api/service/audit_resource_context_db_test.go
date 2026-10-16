package service

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func (f *deployFixture) assertResourceAuditContext(t *testing.T, resourceID uuid.UUID, expectedTypes ...string) {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), "SELECT type, resource_id FROM events WHERE subject_type=ANY($1::text[])",
		[]string{events.SubjectResource, events.SubjectDeployment, events.SubjectDomain})
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var types []string
	for rows.Next() {
		var kind string
		var actual *uuid.UUID
		if err := rows.Scan(&kind, &actual); err != nil {
			t.Fatal(err)
		}
		if actual == nil || *actual != resourceID {
			t.Errorf("%s resource=%v, want %s", kind, actual, resourceID)
		}
		types = append(types, kind)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range expectedTypes {
		if !slices.Contains(types, expected) {
			t.Errorf("audit types=%v, missing %s", types, expected)
		}
	}
}

func TestImperativeChildAuditEventsRetainResource(t *testing.T) {
	f := newDeployFixture(t)
	client := newDomainClient(t, f)
	if err := client.add(testPrimaryDomain); err != nil {
		t.Fatal(err)
	}
	if err := client.removeByID(client.domainID(t, testPrimaryDomain)); err != nil {
		t.Fatal(err)
	}
	created, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	server := NewDeploymentServer(
		f.pool,
		f.queries,
		&fakeResolver{digest: testDigest},
		testRegistryHost,
		testServiceDefaults(),
		testClusterSilentAfter,
	)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
	if _, err := server.DeleteDeployment(
		ctx,
		&deploymentv1.DeleteDeploymentRequest{DeploymentId: created.GetDeploymentId()},
	); err != nil {
		t.Fatal(err)
	}
	f.assertResourceAuditContext(
		t,
		f.resourceID,
		events.DomainCreated,
		events.DomainDeleted,
		events.DeploymentCreated,
		events.DeploymentDeleted,
	)
}

func TestApplyChildAuditEventsRetainResource(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, planFileHeader+applyFileWorker, applyOptions{})
	f.finishRollouts(t)
	changed := strings.Replace(applyFileWorkerDebug, applyWorkerDomain, applyAPIDomain, 1)
	applyOK(t, f, planFileHeader+changed, applyOptions{})
	f.finishRollouts(t)
	resource, found := f.resourceByName(t, applyWorker)
	if !found {
		t.Fatal("worker resource missing")
	}
	applyOK(t, f, emptyServicesFile, applyOptions{confirmDestructive: true})
	f.assertResourceAuditContext(t, resource.ID, events.ResourceCreated, events.ResourceUpdated, events.ResourceDeleted,
		events.DomainCreated, events.DomainUpdated, events.DomainDeleted, events.DeploymentCreated)
	for _, table := range []string{"resources", "resource_domains", "deployments"} {
		key := "resource_id"
		if table == "resources" {
			key = "id"
		}
		var remaining int
		if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE "+key+"=$1", resource.ID).
			Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Errorf("%s has %d rows after resource deletion", table, remaining)
		}
	}
}
