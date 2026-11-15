package service

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
)

// listUserWorkspacesScan is the whole-table formulation the driven-from-scopes query must match.
const listUserWorkspacesScan = `
SELECT w.id FROM workspaces w
WHERE EXISTS (
    SELECT 1 FROM user_scopes us
    WHERE us.user_id = $1 AND us.scope = 'read'
      AND ((us.entity_type = 'workspace' AND us.entity_id = w.id)
        OR (us.entity_type = 'organization' AND us.entity_id = w.org_id)))
ORDER BY w.created_at DESC, w.id DESC`

func TestListWorkspacesForUserMatchesFullScan(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := t.Context()
	queries := genDb.New(pool)

	newID := func(query string, args ...any) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	owner := newID(`INSERT INTO users (email) VALUES ('owner@example.com') RETURNING id`)
	base := time.Now().UTC().Truncate(time.Second)
	orgs := make([]uuid.UUID, 3)
	workspaces := make([][]uuid.UUID, 3)
	for o := range orgs {
		orgs[o] = newID(
			`INSERT INTO organizations (name, created_by) VALUES ($1, $2) RETURNING id`,
			uuid.NewString(), owner,
		)
		for w := range 5 {
			// Pairs share a timestamp so the id tiebreak decides their order.
			createdAt := base.Add(-time.Duration(o*5+w/2) * time.Hour)
			workspaces[o] = append(workspaces[o], newID(
				`INSERT INTO workspaces (org_id, name, created_by, created_at) VALUES ($1, $2, $3, $4) RETURNING id`,
				orgs[o], uuid.NewString(), owner, createdAt,
			))
		}
	}

	grant := func(user uuid.UUID, scope string, entityType genDb.EntityType, entity uuid.UUID) {
		t.Helper()
		_, err := pool.Exec(
			ctx,
			`INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) VALUES ($1, $2, $3, $4)`,
			user, scope, entityType, entity,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	newUser := func(name string) uuid.UUID {
		return newID(`INSERT INTO users (email) VALUES ($1) RETURNING id`, name+"@example.com")
	}
	const (
		wsType  = genDb.EntityTypeWorkspace
		orgType = genDb.EntityTypeOrganization
	)
	type userCase struct {
		user uuid.UUID
		want int
	}
	cases := map[string]userCase{}
	only := newUser("workspace-only")
	grant(only, "read", wsType, workspaces[0][1])
	grant(only, "read", wsType, workspaces[1][4])
	grant(only, "read", wsType, workspaces[2][0])
	cases["workspace scopes only"] = userCase{only, 3}
	orgOnly := newUser("org-only")
	grant(orgOnly, "read", orgType, orgs[0])
	grant(orgOnly, "read", orgType, orgs[2])
	cases["org scopes only"] = userCase{orgOnly, 10}
	both := newUser("both")
	grant(both, "read", orgType, orgs[1])
	grant(both, "read", wsType, workspaces[0][3])
	grant(both, "read", wsType, workspaces[2][2])
	cases["both kinds"] = userCase{both, 7}
	overlap := newUser("overlap")
	grant(overlap, "read", orgType, orgs[0])
	grant(overlap, "read", wsType, workspaces[0][0])
	grant(overlap, "read", wsType, workspaces[0][4])
	grant(overlap, "read", orgType, orgs[1])
	grant(overlap, "read", wsType, workspaces[1][2])
	cases["overlapping scopes"] = userCase{overlap, 10}
	notRead := newUser("not-read")
	grant(notRead, "member", orgType, orgs[0])
	grant(notRead, "write", wsType, workspaces[1][0])
	cases["scopes other than read"] = userCase{notRead, 0}
	cases["no scopes"] = userCase{newUser("none"), 0}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rows, err := pool.Query(ctx, listUserWorkspacesScan, c.user)
			if err != nil {
				t.Fatal(err)
			}
			var want []uuid.UUID
			for rows.Next() {
				var id uuid.UUID
				if err = rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				want = append(want, id)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if len(want) != c.want {
				t.Fatalf("full scan returned %d workspaces, fixture expects %d", len(want), c.want)
			}

			for _, pageSize := range []int32{1, 2, 3, 100} {
				var got []uuid.UUID
				params := genDb.ListWorkspacesForUserParams{UserID: c.user, Limit: pageSize}
				for {
					page, err := queries.ListWorkspacesForUser(ctx, params)
					if err != nil {
						t.Fatal(err)
					}
					for _, ws := range page {
						got = append(got, ws.ID)
					}
					if int32(len(page)) < pageSize {
						break
					}
					last := page[len(page)-1]
					token := last.ID.String()
					params.PageToken = &token
					params.PageCreatedAt = &last.CreatedAt
				}
				if !slices.Equal(got, want) {
					t.Fatalf("page size %d returned %v, want %v", pageSize, got, want)
				}
			}
		})
	}
}
