package service

import (
	"bytes"
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestResourcePaginationSurvivesDeletingTheCursorRow(t *testing.T) {
	f := newDeployFixture(t)
	workspaceID := f.workspaceID(t)
	ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, f.workspaceReadScopes(t))
	position := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	ids := make([]uuid.UUID, 3)
	for i := range ids {
		id, err := f.queries.CreateResource(
			ctx,
			genDb.CreateResourceParams{
				WorkspaceID: workspaceID,
				Name:        "page-" + strconv.Itoa(i),
				Type:        genDb.ResourceTypeService,
				Spec:        []byte(sameRegionSpec),
				SpecVersion: 1,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
		if _, err := f.pool.Exec(ctx, "UPDATE resources SET created_at=$1 WHERE id=$2", position, id); err != nil {
			t.Fatal(err)
		}
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(b[:], a[:]) })
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	page := func(cursor string) ([]*resourcev1.Resource, string) {
		t.Helper()
		response, err := server.ListWorkspaceResources(
			ctx,
			&resourcev1.ListWorkspaceResourcesRequest{
				WorkspaceId: workspaceID.String(),
				PageSize:    1,
				PageToken:   cursor,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return response.GetResources(), response.GetNextPageToken()
	}
	rows, cursor := page("")
	if len(rows) != 1 || rows[0].GetId() != ids[0].String() {
		t.Fatalf("first page=%v", rows)
	}
	if err := f.queries.DeleteResource(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	rows, cursor = page(cursor)
	if len(rows) != 1 || rows[0].GetId() != ids[1].String() {
		t.Fatalf("page after cursor deletion=%v, want %s", rows, ids[1])
	}
	rows, _ = page(cursor)
	if len(rows) != 1 || rows[0].GetId() != ids[2].String() {
		t.Fatalf("third page=%v, want %s", rows, ids[2])
	}
}
