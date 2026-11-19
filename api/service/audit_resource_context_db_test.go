package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

// assertResourceAuditContext checks the resource, deployment and domain events recorded after
// seq name resourceID and include expectedTypes.
func (f *deployFixture) assertResourceAuditContext(
	t *testing.T,
	seq int64,
	resourceID uuid.UUID,
	expectedTypes ...string,
) {
	t.Helper()
	rows, err := f.pool.Query(t.Context(),
		"SELECT type, resource_id FROM events WHERE subject_type=ANY($1::text[]) AND seq > $2",
		[]string{events.SubjectResource, events.SubjectDeployment, events.SubjectDomain}, seq)
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
	t.Parallel()
	f := newDeployFixture(t)
	client := newDomainClient(t, f)
	if err := client.add(testPrimaryDomain); err != nil {
		t.Fatal(err)
	}
	if err := client.removeByID(client.domainID(t, testPrimaryDomain)); err != nil {
		t.Fatal(err)
	}
	created, err := createTestDeployment(t, f, sameRegionSpec)
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
		testMaxPlacementBytes,
		limits.Defaults(),
		8,
		nil,
	)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: scopes})
	if _, err := server.DeleteDeployment(
		ctx,
		&deploymentv1.DeleteDeploymentRequest{DeploymentId: created.GetDeploymentId()},
	); err != nil {
		t.Fatal(err)
	}
	f.assertResourceAuditContext(
		t,
		0,
		f.resourceID,
		events.DomainCreated,
		events.DomainDeleted,
		events.DeploymentCreated,
		events.DeploymentDeleted,
	)
}

func TestApplyChildAuditEventsRetainResource(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	seeded := f.lastEventSeq(t)
	applyOK(t, f, planFileHeader+applyFileWorker, applyOptions{})
	f.finishRollouts(t)
	for _, domains := range []string{applyAPIDomain + ", " + applyWorkerDomain, applyAPIDomain} {
		file := strings.Replace(applyFileWorkerDebug, applyWorkerDomain, domains, 1)
		applyOK(t, f, planFileHeader+file, applyOptions{})
		f.finishRollouts(t)
	}
	resource, found := f.resourceByName(t, applyWorker)
	if !found {
		t.Fatal("worker resource missing")
	}
	applyOK(t, f, emptyServicesFile, applyOptions{confirmDestructive: true})
	f.assertResourceAuditContext(t, seeded, resource.ID,
		events.ResourceCreated, events.ResourceUpdated, events.ResourceDeleted,
		events.DomainCreated, events.DomainUpdated, events.DomainDeleted, events.DeploymentCreated)
	for _, owned := range []struct{ table, key string }{
		{"resources", "id"},
		{"resource_domains", "resource_id"},
		{"deployments", "resource_id"},
	} {
		var remaining int
		if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM "+owned.table+" WHERE "+owned.key+"=$1", resource.ID).
			Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Errorf("%s has %d rows after resource deletion", owned.table, remaining)
		}
	}
}

func (f *deployFixture) lastEventSeq(t *testing.T) int64 {
	t.Helper()
	var seq int64
	if err := f.pool.QueryRow(t.Context(), "SELECT coalesce(max(seq), 0) FROM events").Scan(&seq); err != nil {
		t.Fatal(err)
	}
	return seq
}
