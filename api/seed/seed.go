package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/gen/db"
	queries "github.com/team-loco/loco/api/gen/db"
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
	var userIDs []int64     // len 5
	var orgIDs []int64      // len 2
	var wksIDs []int64      // len 4
	var resourceIds []int64 // len 6
	if userIDs, err = seedUsers(ctx, q); err != nil {
		return err
	}
	if orgIDs, err = seedOrganizations(ctx, q, userIDs); err != nil {
		return err
	}
	if wksIDs, err = seedWorkspaces(ctx, q, orgIDs); err != nil {
		return err
	}
	if resourceIds, err = seedResources(ctx, q, wksIDs, userIDs); err != nil {
		return err
	}
	if err := seedUserScopes(ctx, q, orgIDs, wksIDs, resourceIds, userIDs); err != nil {
		return err
	}
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

func seedUsers(ctx context.Context, queries *db.Queries) ([]int64, error) {
	var userIDs []int64
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

func seedOrganizations(ctx context.Context, queries *db.Queries, userIDs []int64) ([]int64, error) {
	var orgIDs []int64

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

func seedWorkspaces(ctx context.Context, queries *db.Queries, orgIDs []int64) ([]int64, error) {
	var wksIDs []int64

	if wks1, err := queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		OrgID:       orgIDs[0], // org 1
		Name:        "Workspace One",
		Description: opttext("alpha org's first workspace"),
	}); err != nil {
		return nil, fmt.Errorf("creating wks 1: %w", err)
	} else {
		wksIDs = append(wksIDs, wks1)
	}

	if wks2, err := queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		OrgID:       orgIDs[0], // org 1
		Name:        "Workspace Two",
		Description: opttext("alpha org's second workspace"),
	}); err != nil {
		return nil, fmt.Errorf("creating wks 2: %w", err)
	} else {
		wksIDs = append(wksIDs, wks2)
	}

	if wks3, err := queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		OrgID:       orgIDs[1], // org 2
		Name:        "Workspace Three",
		Description: opttext("beta org's first workspace"),
	}); err != nil {
		return nil, fmt.Errorf("creating wks 3: %w", err)
	} else {
		wksIDs = append(wksIDs, wks3)
	}

	if wks4, err := queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		OrgID:       orgIDs[1], // org 2
		Name:        "Workspace Four",
		Description: opttext("beta org's second workspace"),
	}); err != nil {
		return nil, fmt.Errorf("creating wks 4: %w", err)
	} else {
		wksIDs = append(wksIDs, wks4)
	}

	return wksIDs, nil
}

func seedResources(ctx context.Context, queries *db.Queries, wksIDs []int64, userIDs []int64) ([]int64, error) {
	var resourceIds []int64

	if resource1Id, err := queries.CreateResource(ctx, db.CreateResourceParams{
		WorkspaceID: wksIDs[0],              // wks 1
		Name:        "My First Application", // Resource 1
		Type:        db.ResourceTypeService,
		Description: "This is my first application",
		Status:      db.ResourceStatusHealthy,
		Spec:        specExample,
		SpecVersion: 1,
		CreatedBy:   userIDs[2], // user 3
	}); err != nil {
		return nil, fmt.Errorf("creating resource 1: %w", err)
	} else {
		resourceIds = append(resourceIds, resource1Id)
	}

	if resource2Id, err := queries.CreateResource(ctx, db.CreateResourceParams{
		WorkspaceID: wksIDs[0],               // wks 1
		Name:        "My Second Application", // Resource 2
		Type:        db.ResourceTypeService,
		Description: "This is my second application",
		Status:      db.ResourceStatusHealthy,
		Spec:        specExample,
		SpecVersion: 1,
		CreatedBy:   userIDs[0], // user 1
	}); err != nil {
		return nil, fmt.Errorf("creating resource 2: %w", err)
	} else {
		resourceIds = append(resourceIds, resource2Id)
	}

	if resource3Id, err := queries.CreateResource(ctx, db.CreateResourceParams{
		WorkspaceID: wksIDs[1],              // wks 2
		Name:        "My Third Application", // Resource 3
		Type:        db.ResourceTypeService,
		Description: "This is my third application",
		Status:      db.ResourceStatusHealthy,
		Spec:        specExample,
		SpecVersion: 1,
		CreatedBy:   userIDs[2], // user 3
	}); err != nil {
		return nil, fmt.Errorf("creating resource 3: %w", err)
	} else {
		resourceIds = append(resourceIds, resource3Id)
	}

	if resource4ID, err := queries.CreateResource(ctx, db.CreateResourceParams{
		WorkspaceID: wksIDs[1],               // wks 2
		Name:        "My Fourth Application", // Resource 4
		Type:        db.ResourceTypeService,
		Description: "This is my fourth application",
		Status:      db.ResourceStatusHealthy,
		Spec:        specExample,
		SpecVersion: 1,
		CreatedBy:   userIDs[0], // user 1
	}); err != nil {
		return nil, fmt.Errorf("creating resource 4: %w", err)
	} else {
		resourceIds = append(resourceIds, resource4ID)
	}

	if resource5ID, err := queries.CreateResource(ctx, db.CreateResourceParams{
		WorkspaceID: wksIDs[2],              // wks 3
		Name:        "My Fifth Application", // Resource 5
		Type:        db.ResourceTypeService,
		Description: "This is my fifth application",
		Status:      db.ResourceStatusHealthy,
		Spec:        specExample,
		SpecVersion: 1,
		CreatedBy:   userIDs[3], // user 4
	}); err != nil {
		return nil, fmt.Errorf("creating resource 5: %w", err)
	} else {
		resourceIds = append(resourceIds, resource5ID)
	}

	if resource6ID, err := queries.CreateResource(ctx, db.CreateResourceParams{
		WorkspaceID: wksIDs[3],              // wks 4
		Name:        "My Sixth Application", // Resource 6
		Type:        db.ResourceTypeService,
		Description: "This is my sixth application",
		Status:      db.ResourceStatusHealthy,
		Spec:        specExample,
		SpecVersion: 1,
		CreatedBy:   userIDs[1], // user 2
	}); err != nil {
		return nil, fmt.Errorf("creating resource 6: %w", err)
	} else {
		resourceIds = append(resourceIds, resource6ID)
	}

	return resourceIds, nil
}

func seedUserScopes(ctx context.Context, queries *db.Queries, orgIDs, wksIDs, resourceIds, userIDs []int64) error {
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
		{
			UserID:     userIDs[5],
			EntityType: db.EntityTypeSystem,
			EntityID:   0,
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[5],
			EntityType: db.EntityTypeSystem,
			EntityID:   0,
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[5],
			EntityType: db.EntityTypeSystem,
			EntityID:   0,
			Scope:      db.ScopeAdmin,
		},
	}

	allScopes := slices.Concat(user1Scopes, user2Scopes, user3Scopes, user4Scopes, user5Scopes, user6Scopes)
	for _, scope := range allScopes {
		if err := queries.AddUserScope(ctx, scope); err != nil {
			// addiing organization_1:read for user with id 1: bla bla bla
			return fmt.Errorf("adding %s_%d:%s for user with id %d: %w", scope.EntityType, scope.EntityID, scope.Scope, scope.UserID, err)
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

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		slog.Error("unable to create db pool", "error", err)
		return
	}
	defer pool.Close()

	if err := Seed(context.Background(), pool, strings.Split(migrationFiles, ",")); err != nil {
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
	if err := tvmtestupdateroles(pool); err != nil {
		slog.Error("tvm test update roles failed", "error", err)
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
