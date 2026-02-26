package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/gen/db"
	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/providers"
)

// the database we're seeding:
// - 5 users
// - 2 organizations (org 1 and org 2)
// - 4 workspaces (wks 1,2 in org 1, wks 3,4 in org 2)
// - 6 apps (resource 1,2 in wks 1; resource 3,4 in wks 2; resource 5 in wks 3; resource 6 in wks 4)
// Resource 1,2,3,4 -> org 1
// Resource 5,6 -> org 2
// user 1 has org 1 r/w/a
// user 2 has org 2 r/w/a
// user 3 has org:r/w for org 1 and org 2 and resource:rwa for resource 1 and resource 3
// user 4 has wks:read/write for wks 3
// user 5 has resource:read for resource 5 and resource 6
// all users have r/w/a on themselves
// note: no testing of a user w/o r/w/a/ on themselves
// the createdby fields are not set accordingly and they're irrelevant

var specExample, _ = os.ReadFile("spec_example.json")

var globalTestData struct {
	UserIDs      []uuid.UUID
	OrgIDs       []uuid.UUID
	WorkspaceIDs []uuid.UUID
	ResourceIDs  []uuid.UUID
}

func Seed(ctx context.Context, pool *pgxpool.Pool, migrationFiles []string) error {
	// run migrations
	if err := runMigrations(ctx, pool, migrationFiles); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	// start a transaction
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)

	// perform seeding operations here
	var userIDs []uuid.UUID     // len 6
	var orgIDs []uuid.UUID      // len 2
	var wksIDs []uuid.UUID      // len 4
	var resourceIds []uuid.UUID // len 6
	if userIDs, err = seedUsers(ctx, q); err != nil {
		return err
	}
	if orgIDs, err = seedOrganizations(ctx, q, userIDs); err != nil {
		return err
	}
	var envIDs []uuid.UUID // len 4, one production env per workspace
	if wksIDs, envIDs, err = seedWorkspaces(ctx, q, orgIDs, userIDs); err != nil {
		return err
	}
	if resourceIds, err = seedResources(ctx, q, wksIDs, envIDs); err != nil {
		return err
	}
	if err := seedUserScopes(ctx, q, orgIDs, wksIDs, resourceIds, userIDs); err != nil {
		return err
	}

	// store for test functions
	globalTestData.UserIDs = userIDs
	globalTestData.OrgIDs = orgIDs
	globalTestData.WorkspaceIDs = wksIDs
	globalTestData.ResourceIDs = resourceIds

	return tx.Commit(ctx)
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool, filepaths []string) error {
	if len(filepaths) == 0 {
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, path := range filepaths {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration file %s: %w", path, err)
		}
		_, err = tx.Exec(ctx, string(data))
		if err != nil {
			return fmt.Errorf("execute migration file %s: %w", path, err)
		}
	}

	return tx.Commit(ctx)
}

func seedUsers(ctx context.Context, queries *db.Queries) ([]uuid.UUID, error) {
	var userIDs []uuid.UUID
	if user1, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:       opttext("The First"),
		Email:      "user1@example.com",
		ExternalID: "github-user1",
		AvatarUrl:  opttext("https://example.com/avatar1.png"),
	}); err != nil {
		return nil, fmt.Errorf("creating user 1: %w", err)
	} else {
		userIDs = append(userIDs, user1.ID)
	}
	if user2, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:       opttext("The Second"),
		Email:      "user2@example.com",
		ExternalID: "github-user2",
		AvatarUrl:  opttext("https://example.com/avatar2.png"),
	}); err != nil {
		return nil, fmt.Errorf("creating user 2: %w", err)
	} else {
		userIDs = append(userIDs, user2.ID)
	}
	if user3, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:       opttext("The Third"),
		Email:      "user3@example.com",
		ExternalID: "github-user3",
		AvatarUrl:  opttext("https://example.com/avatar3.png"),
	}); err != nil {
		return nil, fmt.Errorf("creating user 3: %w", err)
	} else {
		userIDs = append(userIDs, user3.ID)
	}
	if user4, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:       opttext("The Fourth"),
		Email:      "user4@example.com",
		ExternalID: "github-user4",
		AvatarUrl:  opttext("https://example.com/avatar4.png"),
	}); err != nil {
		return nil, fmt.Errorf("creating user 4: %w", err)
	} else {
		userIDs = append(userIDs, user4.ID)
	}
	if user5, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:       opttext("The Fifth"),
		Email:      "user5@example.com",
		ExternalID: "github-user5",
		AvatarUrl:  opttext("https://example.com/avatar5.png"),
	}); err != nil {
		return nil, fmt.Errorf("creating user 5: %w", err)
	} else {
		userIDs = append(userIDs, user5.ID)
	}
	if user6, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:       opttext("Nikhil Kumar"),
		Email:      "nikumar1206@gmail.com",
		ExternalID: "github-nikumar1206",
		AvatarUrl:  opttext("https://avatars.githubusercontent.com/u/96546721?v=4"),
	}); err != nil {
		return nil, fmt.Errorf("creating user 6: %w", err)
	} else {
		userIDs = append(userIDs, user6.ID)
	}
	return userIDs, nil
}

func seedOrganizations(ctx context.Context, queries *db.Queries, userIDs []uuid.UUID) ([]uuid.UUID, error) {
	var orgIDs []uuid.UUID

	if org1, err := queries.CreateOrganization(ctx, db.CreateOrganizationParams{
		Name:      "Alpha Org",
		CreatedBy: userIDs[0], // user 1 created org 1
	}); err != nil {
		return nil, fmt.Errorf("creating org 1: %w", err)
	} else {
		orgIDs = append(orgIDs, org1.ID)
	}

	if org2, err := queries.CreateOrganization(ctx, db.CreateOrganizationParams{
		Name:      "Beta Org",
		CreatedBy: userIDs[1], // user 2 created org 2
	}); err != nil {
		return nil, fmt.Errorf("creating org 2: %w", err)
	} else {
		orgIDs = append(orgIDs, org2.ID)
	}

	return orgIDs, nil
}

func seedWorkspaces(ctx context.Context, queries *db.Queries, orgIDs []uuid.UUID, userIDs []uuid.UUID) ([]uuid.UUID, []uuid.UUID, error) {
	var wksIDs []uuid.UUID
	var envIDs []uuid.UUID

	type wksSpec struct {
		orgIdx  int
		name    string
		desc    string
		userIdx int
	}
	specs := []wksSpec{
		{0, "Workspace One", "alpha org's first workspace", 0},
		{0, "Workspace Two", "alpha org's second workspace", 0},
		{1, "Workspace Three", "beta org's first workspace", 1},
		{1, "Workspace Four", "beta org's second workspace", 1},
	}

	for i, s := range specs {
		wsID, err := queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
			OrgID:       orgIDs[s.orgIdx],
			Name:        s.name,
			Description: opttext(s.desc),
			CreatedBy:   userIDs[s.userIdx],
		})
		if err != nil {
			return nil, nil, fmt.Errorf("creating wks %d: %w", i+1, err)
		}
		wksIDs = append(wksIDs, wsID)

		env, err := queries.CreateEnvironment(ctx, db.CreateEnvironmentParams{
			WorkspaceID:  wsID,
			Name:         "production",
			IsProduction: true,
			CreatedBy:    userIDs[s.userIdx],
		})
		if err != nil {
			return nil, nil, fmt.Errorf("creating production env for wks %d: %w", i+1, err)
		}
		envIDs = append(envIDs, env.ID)
	}

	return wksIDs, envIDs, nil
}

func seedResources(ctx context.Context, queries *db.Queries, wksIDs []uuid.UUID, envIDs []uuid.UUID) ([]uuid.UUID, error) {
	var resourceIds []uuid.UUID

	type resSpec struct {
		wksIdx int
		name   string
		desc   string
	}
	specs := []resSpec{
		{0, "My First Application", "This is my first application"},
		{0, "My Second Application", "This is my second application"},
		{1, "My Third Application", "This is my third application"},
		{1, "My Fourth Application", "This is my fourth application"},
		{2, "My Fifth Application", "This is my fifth application"},
		{3, "My Sixth Application", "This is my sixth application"},
	}

	for i, s := range specs {
		id, err := queries.CreateResource(ctx, db.CreateResourceParams{
			WorkspaceID:   wksIDs[s.wksIdx],
			Name:          s.name,
			Type:          db.ResourceTypeService,
			Description:   s.desc,
			Status:        db.ResourceStatusHealthy,
			Spec:          specExample,
			SpecVersion:   1,
		})
		if err != nil {
			return nil, fmt.Errorf("creating resource %d: %w", i+1, err)
		}
		resourceIds = append(resourceIds, id)
	}

	return resourceIds, nil
}

func seedUserScopes(ctx context.Context, queries *db.Queries, orgIDs, wksIDs, resourceIds, userIDs []uuid.UUID) error {
	// user 1: org 1 r/w/a
	user1Scopes := []db.AddUserScopeParams{
		{
			UserID:     userIDs[0],
			EntityType: db.EntityTypeOrganization,
			EntityID:   orgIDs[0],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[0],
			EntityType: db.EntityTypeOrganization,
			EntityID:   orgIDs[0],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[0],
			EntityType: db.EntityTypeOrganization,
			EntityID:   orgIDs[0],
			Scope:      db.ScopeAdmin,
		},
		{
			UserID:     userIDs[0],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[0],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[0],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[0],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[0],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[0],
			Scope:      db.ScopeAdmin,
		},
	}
	// user 2: org 2 r/w/a
	user2Scopes := []db.AddUserScopeParams{
		{
			UserID:     userIDs[1],
			EntityType: db.EntityTypeOrganization,
			EntityID:   orgIDs[1],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[1],
			EntityType: db.EntityTypeOrganization,
			EntityID:   orgIDs[1],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[1],
			EntityType: db.EntityTypeOrganization,
			EntityID:   orgIDs[1],
			Scope:      db.ScopeAdmin,
		},
		{
			UserID:     userIDs[1],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[1],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[1],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[1],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[1],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[1],
			Scope:      db.ScopeAdmin,
		},
	}
	// user 3: org 1 r/w, org 2 r/w, resource 1 r/w/a, resource 3 r/w/a
	user3Scopes := []db.AddUserScopeParams{
		// org 1 r/w
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeOrganization,
			EntityID:   orgIDs[0],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeOrganization,
			EntityID:   orgIDs[0],
			Scope:      db.ScopeWrite,
		},
		// org 2 r/w
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeOrganization,
			EntityID:   orgIDs[1],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeOrganization,
			EntityID:   orgIDs[1],
			Scope:      db.ScopeWrite,
		},
		// resource 1 r/w/a
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeResource,
			EntityID:   resourceIds[0],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeResource,
			EntityID:   resourceIds[0],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeResource,
			EntityID:   resourceIds[0],
			Scope:      db.ScopeAdmin,
		},
		// resource 3 r/w/a
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeResource,
			EntityID:   resourceIds[2],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeResource,
			EntityID:   resourceIds[2],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeResource,
			EntityID:   resourceIds[2],
			Scope:      db.ScopeAdmin,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[2],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[2],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[2],
			Scope:      db.ScopeAdmin,
		},
	}
	// user 4: wks 3 r/w
	user4Scopes := []db.AddUserScopeParams{
		{
			UserID:     userIDs[3],
			EntityType: db.EntityTypeWorkspace,
			EntityID:   wksIDs[2],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[3],
			EntityType: db.EntityTypeWorkspace,
			EntityID:   wksIDs[2],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[3],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[3],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[3],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[3],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[3],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[3],
			Scope:      db.ScopeAdmin,
		},
	}
	// user 5: resource 5 r, resource 6 r
	user5Scopes := []db.AddUserScopeParams{
		{
			UserID:     userIDs[4],
			EntityType: db.EntityTypeResource,
			EntityID:   resourceIds[4],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[4],
			EntityType: db.EntityTypeResource,
			EntityID:   resourceIds[5],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[4],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[4],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[4],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[4],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[4],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[4],
			Scope:      db.ScopeAdmin,
		},
	}
	user6Scopes := []db.AddUserScopeParams{
		{
			UserID:     userIDs[5],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[5],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[5],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[5],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[5],
			EntityType: db.EntityTypeUser,
			EntityID:   userIDs[5],
			Scope:      db.ScopeAdmin,
		},
	}

	allScopes := slices.Concat(user1Scopes, user2Scopes, user3Scopes, user4Scopes, user5Scopes, user6Scopes)
	for _, scope := range allScopes {
		if err := queries.AddUserScope(ctx, scope); err != nil {
			// addiing organization_1:read for user with id 1: bla bla bla
			return fmt.Errorf("adding %s_%v:%s for user with id %v: %w", scope.EntityType, scope.EntityID, scope.Scope, scope.UserID, err)
		}
	}
	return nil
}

// func seedClusters(ctx context.Context, q *db.Queries) error {
// 	q.Clu
// }

func opttext(s string) pgtype.Text {
	return pgtype.Text{Valid: true, String: s}
}

func main() {
	migrationFiles := os.Getenv("MIGRATION_FILES")
	if migrationFiles == "" {
		slog.Error("MIGRATION_FILES environment variable is not set")
		return
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		slog.Error("DATABASE_URL environment variable is not set")
		return
	}

	ctx := context.Background()

	// Run migrations with a plain connection — the custom PG types (entity_type,
	// entity_scope) don't exist yet, so we cannot use the AfterConnect hook here.
	plainConn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		slog.Error("unable to connect for migrations", "error", err)
		return
	}
	for _, path := range strings.Split(migrationFiles, ",") {
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Error("read migration file", "path", path, "error", err)
			plainConn.Close(ctx)
			return
		}
		if _, err := plainConn.Exec(ctx, string(data)); err != nil {
			slog.Error("execute migration", "path", path, "error", err)
			plainConn.Close(ctx)
			return
		}
	}
	plainConn.Close(ctx)

	// Now that the schema (and custom types) exist, create the typed pool.
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		slog.Error("unable to parse db config", "error", err)
		return
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		for _, typeName := range []string{"entity_type", "entity_scope"} {
			t, err := conn.LoadType(ctx, typeName)
			if err != nil {
				return fmt.Errorf("load pg type %q: %w", typeName, err)
			}
			conn.TypeMap().RegisterType(t)
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		slog.Error("unable to create db pool", "error", err)
		return
	}
	defer pool.Close()

	// Seed data (inserts only — no composite type queries needed).
	if err := Seed(ctx, pool, nil); err != nil {
		slog.Error("seeding failed", "error", err)
	}

	if err := tvmtestuser1(pool); err != nil {
		slog.Error("tvm test user 1 failed", "error", err)
	}
	if err := tvmtestuser2(pool); err != nil {
		slog.Error("tvm test user 2 failed", "error", err)
	}
	if err := tvmtestuser3(pool); err != nil {
		slog.Error("tvm test user 3 failed", "error", err)
	}
	if err := tvmtestuser4(pool); err != nil {
		slog.Error("tvm test user 4 failed", "error", err)
	}
	if err := tvmtestuser5(pool); err != nil {
		slog.Error("tvm test user 5 failed", "error", err)
	}
}

func subtest(ctx context.Context, name string, f func(context.Context) error) {
	if ctx.Err() != nil {
		fmt.Println("⏭️", name, "skipped due to parent context error:", ctx.Err())
		return
	}

	t := time.Now()
	if err := f(ctx); err != nil {
		fmt.Println("❌", name, "failed:", err.Error(), "duration", time.Since(t))
		return
	}
	fmt.Println("✅", "name", name, "duration", time.Since(t))
}

// user 1 has org 1 r/w/a
func tvmtestuser1(pool *pgxpool.Pool) error {
	machine := tvm.NewVendingMachine(pool, db.New(pool), tvm.Config{
		MaxAPITokenDuration:         24 * time.Hour * 365,
		SessionAccessTokenDuration:  24 * time.Hour,
		SessionRefreshTokenDuration: 24 * time.Hour * 30,
		LastUsedUpdateInterval:      5 * time.Minute,
	})

	ctx := context.Background()
	user, token, _, err := machine.Exchange(ctx, providers.NewEmailResponse("user1@example.com", nil), "", "")
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}

	fmt.Printf("\n=== Testing User 1 (ID: %s) - org 1 r/w/a ===\n", user.ID.String())

	subtest(ctx, "exchange successful", func(ctx context.Context) error {
		if user.ID != globalTestData.UserIDs[0] {
			return fmt.Errorf("expected user ID %s, got %s", globalTestData.UserIDs[0].String(), user.ID.String())
		}
		if token == "" {
			return fmt.Errorf("token is empty")
		}
		return nil
	})

	// Org 1 permissions - should have full access
	subtest(ctx, "granted org 1 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[0],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted org 1 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[0],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted org 1 admin", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[0],
			Scope:      queries.ScopeAdmin,
		})
	})

	// Org 2 - should be denied
	subtest(ctx, "denied org 2 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[1],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Workspaces via org 1 inheritance (assuming ws 1,2 are in org 1)
	subtest(ctx, "granted workspace 1 read via org 1", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[0],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted workspace 1 write via org 1", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[0],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted workspace 2 admin via org 1", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[1],
			Scope:      queries.ScopeAdmin,
		})
	})

	// Workspace 3 should be denied (different org)
	subtest(ctx, "denied workspace 3 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[2],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Resources via org 1 inheritance
	subtest(ctx, "granted resource 1 read via org 1", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[0],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 2 write via org 1", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[1],
			Scope:      queries.ScopeWrite,
		})
	})

	// Self access
	subtest(ctx, "granted self read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   globalTestData.UserIDs[0],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "denied other user read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   globalTestData.UserIDs[1],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	return nil
}

// user 2 has org 2 r/w/a
func tvmtestuser2(pool *pgxpool.Pool) error {
	machine := tvm.NewVendingMachine(pool, db.New(pool), tvm.Config{
		MaxAPITokenDuration:         24 * time.Hour * 365,
		SessionAccessTokenDuration:  24 * time.Hour,
		SessionRefreshTokenDuration: 24 * time.Hour * 30,
		LastUsedUpdateInterval:      5 * time.Minute,
	})

	ctx := context.Background()
	user, token, _, err := machine.Exchange(ctx, providers.NewEmailResponse("user2@example.com", nil), "", "")
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}

	fmt.Printf("\n=== Testing User 2 (ID: %s) - org 2 r/w/a ===\n", user.ID.String())

	subtest(ctx, "exchange successful", func(ctx context.Context) error {
		if user.ID != globalTestData.UserIDs[1] {
			return fmt.Errorf("expected user ID %s, got %s", globalTestData.UserIDs[1].String(), user.ID.String())
		}
		if token == "" {
			return fmt.Errorf("token is empty")
		}
		return nil
	})

	// Org 2 permissions - should have full access
	subtest(ctx, "granted org 2 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[1],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted org 2 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[1],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted org 2 admin", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[1],
			Scope:      queries.ScopeAdmin,
		})
	})

	// Org 1 - should be denied
	subtest(ctx, "denied org 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[0],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Workspace 3 via org 2 (assuming ws 3 is in org 2)
	subtest(ctx, "granted workspace 3 read via org 2", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[2],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted workspace 3 write via org 2", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[2],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted workspace 3 admin via org 2", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[2],
			Scope:      queries.ScopeAdmin,
		})
	})

	// Workspace 1 should be denied (org 1)
	subtest(ctx, "denied workspace 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[0],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Resources via org 2
	subtest(ctx, "granted resource 5 admin via org 2", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[4],
			Scope:      queries.ScopeAdmin,
		})
	})

	subtest(ctx, "granted resource 6 write via org 2", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[5],
			Scope:      queries.ScopeWrite,
		})
	})

	// Resources not via org 2
	subtest(ctx, "denied resource 3 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[2],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied resource 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[0],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Self access
	subtest(ctx, "granted self read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   globalTestData.UserIDs[1],
			Scope:      queries.ScopeRead,
		})
	})

	return nil
}

// user 3 has org:r/w for org 1 and org 2 and resource:rwa for resource 1 and resource 3
func tvmtestuser3(pool *pgxpool.Pool) error {
	machine := tvm.NewVendingMachine(pool, db.New(pool), tvm.Config{
		MaxAPITokenDuration:         24 * time.Hour * 365,
		SessionAccessTokenDuration:  24 * time.Hour,
		SessionRefreshTokenDuration: 24 * time.Hour * 30,
		LastUsedUpdateInterval:      5 * time.Minute,
	})

	ctx := context.Background()
	user, token, _, err := machine.Exchange(ctx, providers.NewEmailResponse("user3@example.com", nil), "", "")
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}

	fmt.Printf("\n=== Testing User 3 (ID: %s) - org 1&2 r/w, resource 1&3 r/w/a ===\n", user.ID.String())

	subtest(ctx, "exchange successful", func(ctx context.Context) error {
		if user.ID != globalTestData.UserIDs[2] {
			return fmt.Errorf("expected user ID %s, got %s", globalTestData.UserIDs[2].String(), user.ID.String())
		}
		if token == "" {
			return fmt.Errorf("token is empty")
		}
		return nil
	})

	// Org 1 - read/write only, no admin
	subtest(ctx, "granted org 1 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[0],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted org 1 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[0],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied org 1 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[0],
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Org 2 - read/write only, no admin
	subtest(ctx, "granted org 2 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[1],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted org 2 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[1],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied org 2 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[1],
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Workspaces via org permissions
	subtest(ctx, "granted workspace 1 read via org 1", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[0],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted workspace 1 write via org 1", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[0],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied workspace 1 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[0],
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "granted workspace 3 read via org 2", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[2],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted workspace 3 write via org 2", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[2],
			Scope:      queries.ScopeWrite,
		})
	})

	// Resources: r/w/a for resource 1, r/w for resource 2, r/w/a for resource 3, r/w for resource 4, r/w for resource 5, r/w for resource 6
	subtest(ctx, "granted resource 1 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[0],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 1 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[0],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted resource 1 admin", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[0],
			Scope:      queries.ScopeAdmin,
		})
	})

	subtest(ctx, "granted resource 2 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[1],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 2 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[1],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied resource 2 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[1],
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "granted resource 3 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[2],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 3 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[2],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted resource 3 admin", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[2],
			Scope:      queries.ScopeAdmin,
		})
	})

	subtest(ctx, "granted resource 4 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[3],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 4 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[3],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied resource 4 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[3],
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "granted resource 5 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[4],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 5 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[4],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied resource 5 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[4],
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "granted resource 6 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[5],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 6 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[5],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied resource 6 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[5],
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	return nil
}

// user 4 has wks:read/write for wks 3
func tvmtestuser4(pool *pgxpool.Pool) error {
	machine := tvm.NewVendingMachine(pool, db.New(pool), tvm.Config{
		MaxAPITokenDuration:         24 * time.Hour * 365,
		SessionAccessTokenDuration:  24 * time.Hour,
		SessionRefreshTokenDuration: 24 * time.Hour * 30,
		LastUsedUpdateInterval:      5 * time.Minute,
	})

	ctx := context.Background()
	user, token, _, err := machine.Exchange(ctx, providers.NewEmailResponse("user4@example.com", nil), "", "")
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}

	fmt.Printf("\n=== Testing User 4 (ID: %s) - wks 3 r/w ===\n", user.ID.String())

	subtest(ctx, "exchange successful", func(ctx context.Context) error {
		if user.ID != globalTestData.UserIDs[3] {
			return fmt.Errorf("expected user ID %s, got %s", globalTestData.UserIDs[3].String(), user.ID.String())
		}
		if token == "" {
			return fmt.Errorf("token is empty")
		}
		return nil
	})

	// Workspace 3 - read/write only
	subtest(ctx, "granted workspace 3 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[2],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted workspace 3 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[2],
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied workspace 3 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[2],
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Other workspaces - denied
	subtest(ctx, "denied workspace 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[0],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied workspace 2 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[1],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Organizations - denied
	subtest(ctx, "denied org 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[0],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied org 2 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[1],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Resource 5 via workspace 3 (read/write inherited)
	subtest(ctx, "granted resource 5 read via workspace 3", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[4],
			Scope:      queries.ScopeRead,
		})
	})
	subtest(ctx, "granted resource 5 write via workspace 3", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[4],
			Scope:      queries.ScopeWrite,
		})
	})
	subtest(ctx, "denied resource 5 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[4],
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied resource 3 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[2],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})
	subtest(ctx, "denied resource 3 write via workspace 3", func(ctx context.Context) error {
		if err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[2],
			Scope:      queries.ScopeWrite,
		}); err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied resource 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[0],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Self access
	subtest(ctx, "granted self read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   globalTestData.UserIDs[3],
			Scope:      queries.ScopeRead,
		})
	})

	return nil
}

// user 5 has resource:read for resource 5 and resource 6
func tvmtestuser5(pool *pgxpool.Pool) error {
	machine := tvm.NewVendingMachine(pool, db.New(pool), tvm.Config{
		MaxAPITokenDuration:         24 * time.Hour * 365,
		SessionAccessTokenDuration:  24 * time.Hour,
		SessionRefreshTokenDuration: 24 * time.Hour * 30,
		LastUsedUpdateInterval:      5 * time.Minute,
	})

	ctx := context.Background()
	user, token, _, err := machine.Exchange(ctx, providers.NewEmailResponse("user5@example.com", nil), "", "")
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}

	fmt.Printf("\n=== Testing User 5 (ID: %s) - resource 5&6 r ===\n", user.ID.String())

	subtest(ctx, "exchange successful", func(ctx context.Context) error {
		if user.ID != globalTestData.UserIDs[4] {
			return fmt.Errorf("expected user ID %s, got %s", globalTestData.UserIDs[4].String(), user.ID.String())
		}
		if token == "" {
			return fmt.Errorf("token is empty")
		}
		return nil
	})

	// Resource 5 - read only
	subtest(ctx, "granted resource 5 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[4],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "denied resource 5 write", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[4],
			Scope:      queries.ScopeWrite,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied resource 5 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[4],
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Resource 6 - read only
	subtest(ctx, "granted resource 6 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[5],
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "denied resource 6 write", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[5],
			Scope:      queries.ScopeWrite,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Other apps - denied
	subtest(ctx, "denied resource 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[0],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied resource 2 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[1],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied resource 3 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   globalTestData.ResourceIDs[2],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Organizations - denied
	subtest(ctx, "denied org 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[0],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied org 2 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   globalTestData.OrgIDs[1],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Workspaces - denied
	subtest(ctx, "denied workspace 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[0],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied workspace 2 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[1],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied workspace 3 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   globalTestData.WorkspaceIDs[2],
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	// Self access
	subtest(ctx, "granted self read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   globalTestData.UserIDs[4],
			Scope:      queries.ScopeRead,
		})
	})

	return nil
}

func RunAllTests(pool *pgxpool.Pool) {
	fmt.Println("🚀 Starting TVM Live Permission Tests")
	fmt.Println("======================================")

	if err := tvmtestuser1(pool); err != nil {
		fmt.Printf("User 1 test suite failed: %v\n", err)
	}

	if err := tvmtestuser2(pool); err != nil {
		fmt.Printf("User 2 test suite failed: %v\n", err)
	}

	if err := tvmtestuser3(pool); err != nil {
		fmt.Printf("User 3 test suite failed: %v\n", err)
	}

	if err := tvmtestuser4(pool); err != nil {
		fmt.Printf("User 4 test suite failed: %v\n", err)
	}

	if err := tvmtestuser5(pool); err != nil {
		fmt.Printf("User 5 test suite failed: %v\n", err)
	}

	fmt.Println("\n======================================")
	fmt.Println("✨ All test suites completed")
}
