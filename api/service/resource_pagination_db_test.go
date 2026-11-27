package service

import (
	"slices"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestResourcePaginationSurvivesDeletingTheCursorRow(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	workspaceID := f.workspaceID(t)
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: f.workspaceReadScopes(t)})
	ids := make([]uuid.UUID, 3)
	for i := range ids {
		ids[i] = f.otherResource(t, "page-"+strconv.Itoa(i))
	}
	slices.Reverse(ids)
	server := NewResourceServer(
		f.pool,
		f.queries,
		testServiceDefaults(),
		testClusterSilentAfter,
		limits.Defaults(),
		nil,
	)
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
