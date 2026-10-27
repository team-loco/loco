package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

type orgDeleteFixture struct {
	*eventFixture
	workspaceID string
	orgs        *OrgServer
	resources   *ResourceServer
}

func newOrgDeleteFixture(t *testing.T) *orgDeleteFixture {
	t.Helper()
	f := newEventFixture(t)
	server := NewWorkspaceServer(f.pool, f.queries, f.machine)
	created, err := server.CreateWorkspace(
		f.owner,
		&workspacev1.CreateWorkspaceRequest{OrgId: f.orgID, Name: "org-guard-workspace"},
	)
	if err != nil {
		t.Fatal(err)
	}
	org, err := f.queries.GetOrgByID(t.Context(), uuid.MustParse(f.orgID))
	if err != nil {
		t.Fatal(err)
	}
	f.owner = scoped(t, f.queries, org.CreatedBy)
	return &orgDeleteFixture{
		eventFixture: f, workspaceID: created.GetWorkspaceId(),
		orgs:      NewOrgServer(f.pool, f.queries, f.machine, InvitationConfig{}),
		resources: NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter),
	}
}

func (f *orgDeleteFixture) waitForWaiters(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(lockTimeout)
	for time.Now().Before(deadline) {
		var waiting int
		err := f.pool.QueryRow(t.Context(),
			"SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'",
		).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= want {
			return
		}
		time.Sleep(lockPollInterval)
	}
	t.Fatalf("fewer than %d operations waited on a lock", want)
}

func (f *orgDeleteFixture) createResource(t *testing.T) (string, error) {
	t.Helper()
	created, err := f.resources.CreateResource(f.owner, &resourcev1.CreateResourceRequest{
		WorkspaceId: f.workspaceID, Name: "new-resource", Type: resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec: testResourceSpec(),
	})
	return created.GetResourceId(), err
}

func TestOrgDeleteRetainsAResourceCreatedDuringDeletion(t *testing.T) {
	for _, environments := range []bool{true, false} {
		name := "with environments"
		if !environments {
			name = "without environments"
		}
		t.Run(name, func(t *testing.T) {
			f := newOrgDeleteFixture(t)
			if !environments {
				if _, err := f.pool.Exec(
					t.Context(),
					"DELETE FROM environments WHERE workspace_id=$1",
					f.workspaceID,
				); err != nil {
					t.Fatal(err)
				}
			}
			gate, err := f.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err = gate.Exec(
				t.Context(),
				"SELECT id FROM organizations WHERE id=$1 FOR UPDATE",
				f.orgID,
			); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, deleteErr := f.orgs.DeleteOrg(f.owner, &orgv1.DeleteOrgRequest{OrgId: f.orgID})
				done <- deleteErr
			}()
			f.waitForWaiters(t, 1)
			id, createErr := f.createResource(t)
			if createErr != nil {
				t.Fatal(createErr)
			}
			if err := gate.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			wantCode(t, <-done, connect.CodeFailedPrecondition)
			if _, getErr := f.queries.GetResourceByID(t.Context(), uuid.MustParse(id)); getErr != nil {
				t.Fatalf("accepted resource disappeared: %v", getErr)
			}
		})
	}
}

func TestOrgDeleteWaitsForEnvironmentWriterBeforeCascading(t *testing.T) {
	f := newOrgDeleteFixture(t)
	writer, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	qtx := genDb.New(writer)
	if _, err := qtx.LockWorkspaceEnvironments(t.Context(), uuid.MustParse(f.workspaceID)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, deleteErr := f.orgs.DeleteOrg(f.owner, &orgv1.DeleteOrgRequest{OrgId: f.orgID})
		done <- deleteErr
	}()
	f.waitForWaiters(t, 1)
	_, createErr := qtx.CreateResource(t.Context(), genDb.CreateResourceParams{
		WorkspaceID: uuid.MustParse(f.workspaceID), Name: "new-resource", Type: genDb.ResourceTypeService,
		Spec: []byte(sameRegionSpec), SpecVersion: 1,
	})
	if createErr != nil {
		t.Fatal(createErr)
	}
	if commitErr := writer.Commit(t.Context()); commitErr != nil {
		t.Fatal(commitErr)
	}
	wantCode(t, <-done, connect.CodeFailedPrecondition)
}

func TestCreationReportsMissingParentWhenOrgDeletionWins(t *testing.T) {
	for _, kind := range []genDb.EntityType{genDb.EntityTypeResource, genDb.EntityTypeWorkspace} {
		t.Run(string(kind), func(t *testing.T) {
			f := newOrgDeleteFixture(t)
			gate, err := f.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			if _, err := gate.Exec(t.Context(), "LOCK TABLE events IN SHARE MODE"); err != nil {
				t.Fatal(err)
			}
			deleted := make(chan error, 1)
			go func() {
				_, deleteErr := f.orgs.DeleteOrg(f.owner, &orgv1.DeleteOrgRequest{OrgId: f.orgID})
				deleted <- deleteErr
			}()
			f.waitForWaiters(t, 1)
			created := make(chan error, 1)
			go func() {
				if kind == genDb.EntityTypeResource {
					_, createErr := f.createResource(t)
					created <- createErr
					return
				}
				server := NewWorkspaceServer(f.pool, f.queries, f.machine)
				_, createErr := server.CreateWorkspace(
					f.owner,
					&workspacev1.CreateWorkspaceRequest{OrgId: f.orgID, Name: "new-workspace"},
				)
				created <- createErr
			}()
			f.waitForWaiters(t, 2)
			if err := gate.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			if deleteErr := <-deleted; deleteErr != nil {
				t.Fatal(deleteErr)
			}
			wantCode(t, <-created, connect.CodeNotFound)
			if _, getErr := f.queries.GetOrgByID(
				t.Context(),
				uuid.MustParse(f.orgID),
			); !errors.Is(
				getErr,
				pgx.ErrNoRows,
			) {
				t.Fatalf("organization after successful delete=%v", getErr)
			}
		})
	}
}
