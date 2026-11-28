package service

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	genDb "github.com/team-loco/loco/api/gen/db"
	"google.golang.org/protobuf/proto"
)

func TestResourceEnvironmentRequiresOneWorkspace(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	otherWorkspace := f.tenant.workspace(t, "other-workspace")
	otherEnvironment := f.tenant.defaultEnvironment(t, otherWorkspace)
	for _, workspace := range []string{f.workspaceID(t).String(), otherWorkspace.String()} {
		_, err := f.pool.Exec(
			t.Context(),
			`INSERT INTO resource_environments
(resource_id,environment_id,workspace_id,spec,spec_version)
VALUES ($1,$2,$3,'{}',1)`,
			f.resourceID,
			otherEnvironment,
			workspace,
		)
		pgerr, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || pgerr.Code != "23503" {
			t.Fatalf("cross-workspace configuration error = %v, want foreign key violation", err)
		}
	}
}

func TestRemovingEnvironmentConfigurationKeepsDeploymentHistoryAndRemovesItsDomains(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	created, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	domain := newDomainClient(t, f)
	if err = domain.add("history." + testAppDomain); err != nil {
		t.Fatal(err)
	}
	before := f.getDeployment(t, created.GetDeploymentId())
	if n, deleteErr := f.queries.DeleteResourceEnvironment(
		t.Context(),
		genDb.DeleteResourceEnvironmentParams{ResourceID: f.resourceID, EnvironmentID: f.envID},
	); deleteErr != nil ||
		n != 1 {
		t.Fatalf("delete configuration affected %d rows: %v", n, deleteErr)
	}
	if n := f.count(t, `SELECT count(*) FROM resource_domains WHERE resource_id=$1`); n != 0 {
		t.Fatalf("domains after deleting configuration = %d, want none", n)
	}
	after := f.getDeployment(t, created.GetDeploymentId())
	if !proto.Equal(before.GetSpec(), after.GetSpec()) {
		t.Fatal("deleting desired configuration changed deployment history")
	}
	if _, err = f.queries.GetResourceByID(t.Context(), f.resourceID); err != nil {
		t.Fatalf("shared resource identity was removed: %v", err)
	}
}
