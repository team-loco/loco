package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

type listPage func(t *testing.T, token string) ([]string, string)

func assertPagingSurvivesDeletingTheCursorRow(
	t *testing.T,
	newestFirst []string,
	list listPage,
	deleteRow func(t *testing.T, id string),
) {
	t.Helper()
	ids, token := list(t, "")
	if len(ids) != 1 || ids[0] != newestFirst[0] {
		t.Fatalf("first page = %v, want %s", ids, newestFirst[0])
	}
	deleteRow(t, ids[0])
	ids, _ = list(t, token)
	if len(ids) != 1 || ids[0] != newestFirst[1] {
		t.Fatalf("page after deleting the cursor row = %v, want %s", ids, newestFirst[1])
	}
}

func (f *deployFixture) deleteByID(table string) func(t *testing.T, id string) {
	return func(t *testing.T, id string) {
		t.Helper()
		if _, err := f.pool.Exec(context.Background(), "DELETE FROM "+table+" WHERE id = $1", id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeploymentPagingSurvivesDeletingTheCursorRow(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	var newestFirst []string
	for range 3 {
		params := f.paramsFor(f.clusterID)
		params.IsActive = false
		id, err := f.queries.CreateDeployment(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		newestFirst = append([]string{id.String()}, newestFirst...)
	}
	list := func(t *testing.T, token string) ([]string, string) {
		t.Helper()
		resp := f.listDeployments(t, &deploymentv1.ListDeploymentsRequest{
			ResourceId: f.resourceID.String(),
			PageSize:   1,
			PageToken:  token,
		})
		return deploymentIDs(resp.GetDeployments()), resp.GetNextPageToken()
	}
	assertPagingSurvivesDeletingTheCursorRow(t, newestFirst, list, f.deleteByID("deployments"))
}

func TestBuildPagingSurvivesDeletingTheCursorRow(t *testing.T) {
	f := newBuildFixture(t)
	var newestFirst []string
	for range 3 {
		id := f.create(t, 10).GetBuildId()
		newestFirst = append([]string{id}, newestFirst...)
	}
	list := func(t *testing.T, token string) ([]string, string) {
		t.Helper()
		resp, err := f.server.ListBuilds(f.ctx, &buildv1.ListBuildsRequest{
			ResourceId: f.resourceID.String(),
			PageSize:   1,
			PageToken:  token,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(resp.GetBuilds()))
		for _, b := range resp.GetBuilds() {
			ids = append(ids, b.GetId())
		}
		return ids, resp.GetNextPageToken()
	}
	assertPagingSurvivesDeletingTheCursorRow(t, newestFirst, list, f.deleteByID("builds"))
}

func TestUserPagingSurvivesDeletingTheCursorRow(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)
	var newestFirst []string
	for i := range 3 {
		var id uuid.UUID
		err := f.pool.QueryRow(
			ctx,
			`INSERT INTO users (email, created_at) VALUES ($1, $2) RETURNING id`,
			uuid.NewString()+"@example.com",
			future.Add(time.Duration(i)*time.Minute),
		).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		newestFirst = append([]string{id.String()}, newestFirst...)
	}
	server := NewUserServer(f.pool, f.queries, nil, nil)
	scopes := []genDb.EntityScope{{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeRead}}
	scoped := reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: scopes})
	list := func(t *testing.T, token string) ([]string, string) {
		t.Helper()
		resp, err := server.ListUsers(scoped, &userv1.ListUsersRequest{PageSize: 1, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(resp.GetUsers()))
		for _, u := range resp.GetUsers() {
			ids = append(ids, u.GetId())
		}
		return ids, resp.GetNextPageToken()
	}
	assertPagingSurvivesDeletingTheCursorRow(t, newestFirst, list, f.deleteByID("users"))
}
