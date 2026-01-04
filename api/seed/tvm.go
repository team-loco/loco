package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/gen/db"
	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/providers"
)

// user 1 has org 1 r/w/a
func tvmtestuser1(pool *pgxpool.Pool) error {
	machine := tvm.NewVendingMachine(pool, db.New(pool), tvm.Config{
		MaxTokenDuration:   24 * time.Hour * 365,
		LoginTokenDuration: 24 * time.Hour,
	})

	ctx := context.Background()
	user, token, err := machine.Exchange(ctx, providers.NewEmailResponse("user1@example.com", nil))
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}

	fmt.Printf("\n=== Testing User 1 (ID: %d) - org 1 r/w/a ===\n", user.ID)

	subtest(ctx, "exchange successful", func(ctx context.Context) error {
		if user.ID != 1 {
			return fmt.Errorf("expected user ID 1, got %d", user.ID)
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
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted org 1 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted org 1 admin", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeAdmin,
		})
	})

	// Org 2 - should be denied
	subtest(ctx, "denied org 2 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   2,
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
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted workspace 1 write via org 1", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   1,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted workspace 2 admin via org 1", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   2,
			Scope:      queries.ScopeAdmin,
		})
	})

	// Workspace 3 should be denied (different org)
	subtest(ctx, "denied workspace 3 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   3,
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
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 2 write via org 1", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   2,
			Scope:      queries.ScopeWrite,
		})
	})

	// Self access
	subtest(ctx, "granted self read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "denied other user read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   2,
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
		MaxTokenDuration:   24 * time.Hour * 365,
		LoginTokenDuration: 24 * time.Hour,
	})

	ctx := context.Background()
	user, token, err := machine.Exchange(ctx, providers.NewEmailResponse("user2@example.com", nil))
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}

	fmt.Printf("\n=== Testing User 2 (ID: %d) - org 2 r/w/a ===\n", user.ID)

	subtest(ctx, "exchange successful", func(ctx context.Context) error {
		if user.ID != 2 {
			return fmt.Errorf("expected user ID 2, got %d", user.ID)
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
			EntityID:   2,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted org 2 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   2,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted org 2 admin", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   2,
			Scope:      queries.ScopeAdmin,
		})
	})

	// Org 1 - should be denied
	subtest(ctx, "denied org 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
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
			EntityID:   3,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted workspace 3 write via org 2", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   3,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted workspace 3 admin via org 2", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   3,
			Scope:      queries.ScopeAdmin,
		})
	})

	// Workspace 1 should be denied (org 1)
	subtest(ctx, "denied workspace 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   1,
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
			EntityID:   5,
			Scope:      queries.ScopeAdmin,
		})
	})

	subtest(ctx, "granted resource 6 write via org 2", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   6,
			Scope:      queries.ScopeWrite,
		})
	})

	// Resources not via org 2
	subtest(ctx, "denied resource 3 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   3,
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
			EntityID:   1,
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
			EntityID:   2,
			Scope:      queries.ScopeRead,
		})
	})

	return nil
}

// user 3 has org:r/w for org 1 and org 2 and resource:rwa for resource 1 and resource 3
func tvmtestuser3(pool *pgxpool.Pool) error {
	machine := tvm.NewVendingMachine(pool, db.New(pool), tvm.Config{
		MaxTokenDuration:   24 * time.Hour * 365,
		LoginTokenDuration: 24 * time.Hour,
	})

	ctx := context.Background()
	user, token, err := machine.Exchange(ctx, providers.NewEmailResponse("user3@example.com", nil))
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}

	fmt.Printf("\n=== Testing User 3 (ID: %d) - org 1&2 r/w, resource 1&3 r/w/a ===\n", user.ID)

	subtest(ctx, "exchange successful", func(ctx context.Context) error {
		if user.ID != 3 {
			return fmt.Errorf("expected user ID 3, got %d", user.ID)
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
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted org 1 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied org 1 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
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
			EntityID:   2,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted org 2 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   2,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied org 2 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   2,
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
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted workspace 1 write via org 1", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   1,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied workspace 1 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   1,
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
			EntityID:   3,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted workspace 3 write via org 2", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   3,
			Scope:      queries.ScopeWrite,
		})
	})

	// Resources: r/w/a for resource 1, r/w for resource 2, r/w/a for resource 3, r/w for resource 4, r/w for resource 5, r/w for resource 6
	subtest(ctx, "granted resource 1 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 1 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   1,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted resource 1 admin", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   1,
			Scope:      queries.ScopeAdmin,
		})
	})

	subtest(ctx, "granted resource 2 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   2,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 2 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   2,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied resource 2 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   2,
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
			EntityID:   3,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 3 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   3,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "granted resource 3 admin", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   3,
			Scope:      queries.ScopeAdmin,
		})
	})

	subtest(ctx, "granted resource 4 read", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   4,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 4 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   4,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied resource 4 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   4,
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
			EntityID:   5,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 5 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   5,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied resource 5 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   5,
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
			EntityID:   6,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted resource 6 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   6,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied resource 6 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   6,
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
		MaxTokenDuration:   24 * time.Hour * 365,
		LoginTokenDuration: 24 * time.Hour,
	})

	ctx := context.Background()
	user, token, err := machine.Exchange(ctx, providers.NewEmailResponse("user4@example.com", nil))
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}

	fmt.Printf("\n=== Testing User 4 (ID: %d) - wks 3 r/w ===\n", user.ID)

	subtest(ctx, "exchange successful", func(ctx context.Context) error {
		if user.ID != 4 {
			return fmt.Errorf("expected user ID 4, got %d", user.ID)
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
			EntityID:   3,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "granted workspace 3 write", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   3,
			Scope:      queries.ScopeWrite,
		})
	})

	subtest(ctx, "denied workspace 3 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   3,
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
			EntityID:   1,
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
			EntityID:   2,
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
			EntityID:   1,
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
			EntityID:   2,
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
			EntityID:   5,
			Scope:      queries.ScopeRead,
		})
	})
	subtest(ctx, "granted resource 5 write via workspace 3", func(ctx context.Context) error {
		return machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   5,
			Scope:      queries.ScopeWrite,
		})
	})
	subtest(ctx, "denied resource 5 admin", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   5,
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
			EntityID:   3,
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
			EntityID:   3,
			Scope:      queries.ScopeWrite,
		}); err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}
		return nil
	})

	subtest(ctx, "denied resource 1 read", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   1,
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
			EntityID:   4,
			Scope:      queries.ScopeRead,
		})
	})

	return nil
}

// user 5 has resource:read for resource 5 and resource 6
func tvmtestuser5(pool *pgxpool.Pool) error {
	machine := tvm.NewVendingMachine(pool, db.New(pool), tvm.Config{
		MaxTokenDuration:   24 * time.Hour * 365,
		LoginTokenDuration: 24 * time.Hour,
	})

	ctx := context.Background()
	user, token, err := machine.Exchange(ctx, providers.NewEmailResponse("user5@example.com", nil))
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}

	fmt.Printf("\n=== Testing User 5 (ID: %d) - resource 5&6 r ===\n", user.ID)

	subtest(ctx, "exchange successful", func(ctx context.Context) error {
		if user.ID != 5 {
			return fmt.Errorf("expected user ID 5, got %d", user.ID)
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
			EntityID:   5,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "denied resource 5 write", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   5,
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
			EntityID:   5,
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
			EntityID:   6,
			Scope:      queries.ScopeRead,
		})
	})

	subtest(ctx, "denied resource 6 write", func(ctx context.Context) error {
		err := machine.Verify(ctx, token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   6,
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
			EntityID:   1,
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
			EntityID:   2,
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
			EntityID:   3,
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
			EntityID:   1,
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
			EntityID:   2,
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
			EntityID:   1,
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
			EntityID:   2,
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
			EntityID:   3,
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
			EntityID:   5,
			Scope:      queries.ScopeRead,
		})
	})

	return nil
}

func tvmtestupdateroles(pool *pgxpool.Pool) error {
	ctx := context.Background()
	machine := tvm.NewVendingMachine(pool, db.New(pool), tvm.Config{
		MaxTokenDuration:   24 * time.Hour * 365,
		LoginTokenDuration: 24 * time.Hour,
	})

	_, user1Token, err := machine.Exchange(ctx, providers.NewEmailResponse("user1@example.com", nil))
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}
	_, user2Token, err := machine.Exchange(ctx, providers.NewEmailResponse("user2@example.com", nil))
	if err != nil {
		return fmt.Errorf("exchange failed: %v", err)
	}

	subtest(ctx, "update user 1 scopes", func(ctx context.Context) error {
		// user 1 currently has org 1 r/w/a, user 2 has org 2 r/w/a
		// user 2 will update user 1 to have resource 5 read

		// first, verify user 1 cannot read resource 5
		err := machine.Verify(ctx, user1Token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   5,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			return fmt.Errorf("expected insufficient permissions, got: %v", err)
		}

		err = machine.UpdateMemberRoles(ctx, user2Token, 1, []queries.EntityScope{
			{
				EntityType: queries.EntityTypeResource,
				EntityID:   5,
				Scope:      queries.ScopeRead,
			},
		}, []queries.EntityScope{})
		if err != nil {
			return fmt.Errorf("update roles failed: %v", err)
		}

		err = machine.Verify(ctx, user1Token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   5,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			return fmt.Errorf("expected permission granted, got error: %v", err)
		}

		return nil
	})

	return nil
}
