package service

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestWorkspacePaginationSurvivesDeletingTheCursorRow(t *testing.T) {
	for _, kind := range []string{"organization", "user"} {
		t.Run(kind, func(t *testing.T) {
			f := newDeployFixture(t)
			var orgID, userID uuid.UUID
			query := "SELECT w.org_id, w.created_by FROM workspaces w " +
				"JOIN resources r ON r.workspace_id = w.id WHERE r.id = $1"
			if err := f.pool.QueryRow(t.Context(), query, f.resourceID).
				Scan(&orgID, &userID); err != nil {
				t.Fatal(err)
			}
			scopes := []genDb.EntityScope{
				{EntityType: genDb.EntityTypeOrganization, EntityID: orgID, Scope: genDb.ScopeRead},
				{EntityType: genDb.EntityTypeOrganization, EntityID: orgID, Scope: genDb.ScopeMember},
				{EntityType: genDb.EntityTypeUser, EntityID: userID, Scope: genDb.ScopeRead},
			}
			if _, err := f.pool.Exec(
				t.Context(),
				"INSERT INTO user_scopes (user_id, entity_type, entity_id, scope) VALUES ($1, 'organization', $2, 'read')",
				userID,
				orgID,
			); err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
			ctx = context.WithValue(ctx, contextkeys.EntityKey, genDb.Entity{ID: userID, Type: genDb.EntityTypeUser})
			position := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
			ids := make([]uuid.UUID, 3)
			for i := range ids {
				id, err := f.queries.CreateWorkspace(
					ctx,
					genDb.CreateWorkspaceParams{OrgID: orgID, Name: uuid.NewString(), CreatedBy: userID},
				)
				if err != nil {
					t.Fatal(err)
				}
				ids[i] = id
				if _, err := f.pool.Exec(
					ctx,
					"UPDATE workspaces SET created_at = $1 WHERE id = $2",
					position,
					id,
				); err != nil {
					t.Fatal(err)
				}
			}
			slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(b[:], a[:]) })
			server := NewWorkspaceServer(f.pool, f.queries, nil)
			page := func(cursor string) ([]*workspacev1.Workspace, string) {
				t.Helper()
				if kind == "organization" {
					r, err := server.ListOrgWorkspaces(
						ctx,
						&workspacev1.ListOrgWorkspacesRequest{OrgId: orgID.String(), PageSize: 1, PageToken: cursor},
					)
					if err != nil {
						t.Fatal(err)
					}
					return r.GetWorkspaces(), r.GetNextPageToken()
				}
				r, err := server.ListUserWorkspaces(
					ctx,
					&workspacev1.ListUserWorkspacesRequest{PageSize: 1, PageToken: cursor},
				)
				if err != nil {
					t.Fatal(err)
				}
				return r.GetWorkspaces(), r.GetNextPageToken()
			}
			rows, cursor := page("")
			if len(rows) != 1 || rows[0].GetId() != ids[0].String() {
				t.Fatalf("first page = %v", rows)
			}
			if err := f.queries.RemoveWorkspace(ctx, ids[0]); err != nil {
				t.Fatal(err)
			}
			rows, cursor = page(cursor)
			if len(rows) != 1 || rows[0].GetId() != ids[1].String() {
				t.Fatalf("next page after deleting cursor = %v, want %s", rows, ids[1])
			}
			rows, _ = page(cursor)
			if len(rows) != 1 || rows[0].GetId() != ids[2].String() {
				t.Fatalf("third page = %v, want %s", rows, ids[2])
			}
		})
	}
}
