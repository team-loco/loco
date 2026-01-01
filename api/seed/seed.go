package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/gen/db"
)

// the database we're seeding:
// - 5 users
// - 2 organizations (org 1 and org 2)
// - 4 workspaces (wks 1,2 in org 1, wks 3,4 in org 2)
// - 6 apps (app 1,2 in wks 1; app 3,4 in wks 2; app 5 in wks 3; app 6 in wks 4)
// user 1 has org 1 r/w/a
// user 2 has org 2 r/w/a
// user 3 has org:r/w for org 1 and org 2 and app:rwa for app 1 and app 3
// user 4 has wks:read/write for wks 3
// user 5 has app:read for app 5 and app 6
// the createdby fields are not set accordingly and they're irrelevant

func Seed(ctx context.Context, pool *pgxpool.Pool, migrationFiles []string) error {
	// run migrations
	runMigrations(ctx, pool, migrationFiles)

	// start a transaction
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	queries := db.New(tx)

	// perform seeding operations here
	var userIDs []int64 // len 5
	var orgIDs []int64  // len 2
	var wksIDs []int64  // len 4
	var appIDs []int64  // len 6
	if userIDs, err = seedUsers(ctx, queries); err != nil {
		return err
	}
	if orgIDs, err = seedOrganizations(ctx, queries, userIDs); err != nil {
		return err
	}
	if wksIDs, err = seedWorkspaces(ctx, queries, orgIDs, userIDs); err != nil {
		return err
	}
	if appIDs, err = seedApps(ctx, queries, wksIDs, userIDs); err != nil {
		return err
	}
	if err := seedUserScopes(ctx, queries, orgIDs, wksIDs, appIDs, userIDs); err != nil {
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
		return nil, err
	} else {
		userIDs = append(userIDs, user1.ID)
	}
	if user2, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:       opttext("The Second"),
		Email:      "user2@example.com",
		ExternalID: "github-user2",
		AvatarUrl:  opttext("https://example.com/avatar2.png"),
	}); err != nil {
		return nil, err
	} else {
		userIDs = append(userIDs, user2.ID)
	}
	if user3, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:       opttext("The Third"),
		Email:      "user3@example.com",
		ExternalID: "github-user3",
		AvatarUrl:  opttext("https://example.com/avatar3.png"),
	}); err != nil {
		return nil, err
	} else {
		userIDs = append(userIDs, user3.ID)
	}
	if user4, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:       opttext("The Fourth"),
		Email:      "user4@example.com",
		ExternalID: "github-user4",
		AvatarUrl:  opttext("https://example.com/avatar4.png"),
	}); err != nil {
		return nil, err
	} else {
		userIDs = append(userIDs, user4.ID)
	}
	if user5, err := queries.CreateUser(ctx, db.CreateUserParams{
		Name:       opttext("The Fifth"),
		Email:      "user5@example.com",
		ExternalID: "github-user5",
		AvatarUrl:  opttext("https://example.com/avatar5.png"),
	}); err != nil {
		return nil, err
	} else {
		userIDs = append(userIDs, user5.ID)
	}
	return userIDs, nil
}

func seedOrganizations(ctx context.Context, queries *db.Queries, userIDs []int64) ([]int64, error) {
	var orgIDs []int64

	if org1, err := queries.CreateOrganization(ctx, db.CreateOrganizationParams{
		Name:      "Alpha Org",
		CreatedBy: userIDs[0], // user 1 created org 1
	}); err != nil {
		return nil, err
	} else {
		orgIDs = append(orgIDs, org1.ID)
	}

	if org2, err := queries.CreateOrganization(ctx, db.CreateOrganizationParams{
		Name:      "Beta Org",
		CreatedBy: userIDs[1], // user 2 created org 2
	}); err != nil {
		return nil, err
	} else {
		orgIDs = append(orgIDs, org2.ID)
	}

	return orgIDs, nil
}

func seedWorkspaces(ctx context.Context, queries *db.Queries, orgIDs []int64, userIDs []int64) ([]int64, error) {
	var wksIDs []int64

	if wks1, err := queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		OrgID:       orgIDs[0], // org 1
		Name:        "Workspace One",
		Description: opttext("alpha org's first workspace"),
		CreatedBy:   userIDs[0], // user 1
	}); err != nil {
		return nil, err
	} else {
		wksIDs = append(wksIDs, wks1.ID)
	}

	if wks2, err := queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		OrgID:       orgIDs[0], // org 1
		Name:        "Workspace Two",
		Description: opttext("alpha org's second workspace"),
		CreatedBy:   userIDs[0], // user 1
	}); err != nil {
		return nil, err
	} else {
		wksIDs = append(wksIDs, wks2.ID)
	}

	if wks3, err := queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		OrgID:       orgIDs[1], // org 2
		Name:        "Workspace Three",
		Description: opttext("beta org's first workspace"),
		CreatedBy:   userIDs[1], // user 2
	}); err != nil {
		return nil, err
	} else {
		wksIDs = append(wksIDs, wks3.ID)
	}

	if wks4, err := queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		OrgID:       orgIDs[1], // org 2
		Name:        "Workspace Four",
		Description: opttext("beta org's second workspace"),
		CreatedBy:   userIDs[1], // user 2
	}); err != nil {
		return nil, err
	} else {
		wksIDs = append(wksIDs, wks4.ID)
	}

	return wksIDs, nil
}

func seedApps(ctx context.Context, queries *db.Queries, wksIDs []int64, userIDs []int64) ([]int64, error) {
	var appIDs []int64

	if app1, err := queries.CreateApp(ctx, db.CreateAppParams{
		WorkspaceID: wksIDs[0],              // wks 1
		Name:        "My First Application", // app 1
		CreatedBy:   userIDs[2],             // user 3
	}); err != nil {
		return nil, err
	} else {
		appIDs = append(appIDs, app1.ID)
	}

	if app2, err := queries.CreateApp(ctx, db.CreateAppParams{
		WorkspaceID: wksIDs[0],               // wks 1
		Name:        "My Second Application", // app 2
		CreatedBy:   userIDs[0],              // user 1
	}); err != nil {
		return nil, err
	} else {
		appIDs = append(appIDs, app2.ID)
	}

	if app3, err := queries.CreateApp(ctx, db.CreateAppParams{
		WorkspaceID: wksIDs[1],              // wks 2
		Name:        "My Third Application", // app 3
		CreatedBy:   userIDs[2],             // user 3
	}); err != nil {
		return nil, err
	} else {
		appIDs = append(appIDs, app3.ID)
	}

	if app4, err := queries.CreateApp(ctx, db.CreateAppParams{
		WorkspaceID: wksIDs[1],               // wks 2
		Name:        "My Fourth Application", // app 4
		CreatedBy:   userIDs[0],              // user 1
	}); err != nil {
		return nil, err
	} else {
		appIDs = append(appIDs, app4.ID)
	}

	if app5, err := queries.CreateApp(ctx, db.CreateAppParams{
		WorkspaceID: wksIDs[2],              // wks 3
		Name:        "My Fifth Application", // app 5
		CreatedBy:   userIDs[3],             // user 4
	}); err != nil {
		return nil, err
	} else {
		appIDs = append(appIDs, app5.ID)
	}

	if app6, err := queries.CreateApp(ctx, db.CreateAppParams{
		WorkspaceID: wksIDs[3],              // wks 4
		Name:        "My Sixth Application", // app 6
		CreatedBy:   userIDs[1],             // user 2
	}); err != nil {
		return nil, err
	} else {
		appIDs = append(appIDs, app6.ID)
	}

	return appIDs, nil
}

func seedUserScopes(ctx context.Context, queries *db.Queries, orgIDs, wksIDs, appIDs, userIDs []int64) error {
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
	}
	// user 3: org 1 r/w, org 2 r/w, app 1 r/w/a, app 3 r/w/a
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
		// app 1 r/w/a
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeApp,
			EntityID:   appIDs[0],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeApp,
			EntityID:   appIDs[0],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeApp,
			EntityID:   appIDs[0],
			Scope:      db.ScopeAdmin,
		},
		// app 3 r/w/a
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeApp,
			EntityID:   appIDs[2],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeApp,
			EntityID:   appIDs[2],
			Scope:      db.ScopeWrite,
		},
		{
			UserID:     userIDs[2],
			EntityType: db.EntityTypeApp,
			EntityID:   appIDs[2],
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
	}
	// user 5: app 5 r, app 6 r
	user5Scopes := []db.AddUserScopeParams{
		{
			UserID:     userIDs[4],
			EntityType: db.EntityTypeApp,
			EntityID:   appIDs[4],
			Scope:      db.ScopeRead,
		},
		{
			UserID:     userIDs[4],
			EntityType: db.EntityTypeApp,
			EntityID:   appIDs[5],
			Scope:      db.ScopeRead,
		},
	}

	allScopes := slices.Concat(user1Scopes, user2Scopes, user3Scopes, user4Scopes, user5Scopes)
	for _, scope := range allScopes {
		if err := queries.AddUserScope(ctx, scope); err != nil {
			// addiing organization_1:read for user with id 1: bla bla bla
			return fmt.Errorf("adding %s_%d:%s for user with id %d: %w", scope.EntityType, scope.EntityID, scope.Scope, scope.UserID, err)
		}
	}
	return nil
}

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
}
