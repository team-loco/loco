package tvm_test

import (
	"context"
	"testing"
	"time"

	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/providers"
)

type TestingQueries struct {
	// here's the database:
	// org 1, workspace 1, 2, app 1 in ws 1, app 2 in ws 2
	// org 2, workspace 3, app 3 in ws 3
	// user 1 user1@loco-testing.com: has no scopes
	// user 2 user2@loco-testing.com: r, w, a of org 1
	// user 3 user3@loco-testing.com: r, w of org 1
	// user 4 user4@loco-testing.com: r or ws 1
	// user 5 user5@loco-testing.com: r, w, a of wks 3
	queries.Querier
	tokens map[string]queries.Token
}

func (*TestingQueries) GetUserByEmail(ctx context.Context, email string) (queries.User, error) {
	switch email {
	case "user1@loco-testing.com":
		return queries.User{ID: 1, Email: email}, nil
	case "user2@loco-testing.com":
		return queries.User{ID: 2, Email: email}, nil
	case "user3@loco-testing.com":
		return queries.User{ID: 3, Email: email}, nil
	case "user4@loco-testing.com":
		return queries.User{ID: 4, Email: email}, nil
	case "user5@loco-testing.com":
		return queries.User{ID: 5, Email: email}, nil
	default:
		return queries.User{}, tvm.ErrUserNotFound
	}
}
func (*TestingQueries) GetUserScopes(ctx context.Context, userID int64) ([]queries.EntityScope, error) {
	switch userID {
	case 1:
		return []queries.EntityScope{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: userID},
		}, nil
	case 2:
		return []queries.EntityScope{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: 1},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: 1},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeOrganization, EntityID: 1},
		}, nil
	case 3:
		return []queries.EntityScope{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: 1},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: 1},
		}, nil
	case 4:
		return []queries.EntityScope{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: 1},
		}, nil
	case 5:
		return []queries.EntityScope{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: userID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: 3},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeWorkspace, EntityID: 3},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeWorkspace, EntityID: 3},
		}, nil
	default:
		return nil, tvm.ErrUserNotFound
	}
}
func (tq *TestingQueries) GetUserScopesByEmail(ctx context.Context, email string) ([]queries.EntityScope, error) {
	switch email {
	case "user1@loco-testing.com":
		return tq.GetUserScopes(ctx, 1)
	case "user2@loco-testing.com":
		return tq.GetUserScopes(ctx, 2)
	case "user3@loco-testing.com":
		return tq.GetUserScopes(ctx, 3)
	case "user4@loco-testing.com":
		return tq.GetUserScopes(ctx, 4)
	case "user5@loco-testing.com":
		return tq.GetUserScopes(ctx, 5)
	default:
		return nil, tvm.ErrUserNotFound
	}
}
func (tq *TestingQueries) GetUserWithScopesByEmail(ctx context.Context, email string) (queries.UserWithScopesView, error) {
	user, err := tq.GetUserByEmail(ctx, email)
	if err != nil {
		return queries.UserWithScopesView{}, err
	}
	scopes, err := tq.GetUserScopesByEmail(ctx, email)
	if err != nil {
		return queries.UserWithScopesView{}, err
	}
	return queries.UserWithScopesView{
		ID:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		AvatarUrl: user.AvatarUrl,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
		Scopes:    scopes,
	}, nil
}

func (*TestingQueries) GetOrganizationIDByWorkspaceID(ctx context.Context, id int64) (int64, error) {
	switch id {
	case 1, 2:
		return 1, nil
	case 3:
		return 2, nil
	default:
		return 0, tvm.ErrEntityNotFound
	}
}

func (*TestingQueries) GetWorkspaceOrganizationIDByResourceID(ctx context.Context, id int64) (queries.GetWorkspaceOrganizationIDByResourceIDRow, error) {
	switch id {
	case 1:
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: 1,
			OrgID:       1,
		}, nil
	case 2:
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: 2,
			OrgID:       1,
		}, nil
	case 3:
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: 3,
			OrgID:       2,
		}, nil
	default:
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{}, tvm.ErrEntityNotFound
	}
}

func (tq *TestingQueries) StoreToken(ctx context.Context, params queries.StoreTokenParams) error {
	tq.tokens[params.Token] = queries.Token{
		Scopes:     params.Scopes,
		EntityID:   params.EntityID,
		EntityType: params.EntityType,
		ExpiresAt:  params.ExpiresAt,
	}
	return nil
}

func (tq *TestingQueries) GetToken(ctx context.Context, token string) (queries.Token, error) {
	tk, ok := tq.tokens[token]
	if !ok {
		return queries.Token{}, tvm.ErrTokenNotFound
	}
	return tk, nil
}

func (tq *TestingQueries) DeleteToken(ctx context.Context, token string) error {
	delete(tq.tokens, token)
	return nil
}

func (tq *TestingQueries) DeleteExpiredTokens(ctx context.Context) error {
	now := time.Now()
	for token, tk := range tq.tokens {
		if tk.ExpiresAt.Before(now) {
			delete(tq.tokens, token)
		}
	}
	return nil
}
func TestingGithubProvider(ctx context.Context, token string) providers.EmailResponse {
	switch token {
	case "github-token-user1":
		return providers.NewEmail("user1@loco-testing.com", nil)
	case "github-token-user2":
		return providers.NewEmail("user2@loco-testing.com", nil)
	case "github-token-user3":
		return providers.NewEmail("user3@loco-testing.com", nil)
	case "github-token-user4":
		return providers.NewEmail("user4@loco-testing.com", nil)
	case "github-token-user5":
		return providers.NewEmail("user5@loco-testing.com", nil)
	}
	return providers.NewEmail("", tvm.ErrUserNotFound)
}

// user 1 has only self read/write/admin
func TestUser1Permissions(t *testing.T) {
	machine := tvm.NewVendingMachine(nil, &TestingQueries{tokens: make(map[string]queries.Token)}, tvm.Config{
		MaxTokenDuration:   24 * time.Hour,
		LoginTokenDuration: 15 * time.Minute,
	})
	_, token, err := machine.Exchange(t.Context(), TestingGithubProvider(t.Context(), "github-token-user1"))
	if err != nil {
		t.Fatalf("unexpected error during exchange: %v", err)
	}

	t.Run("denied org 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied workspace 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted self read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for self read, got: %v", err)
		}
	})

	t.Run("denied other user read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   2,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 2 has org 1 r, w, a
func TestUser2Permissions(t *testing.T) {
	machine := tvm.NewVendingMachine(nil, &TestingQueries{tokens: make(map[string]queries.Token)}, tvm.Config{
		MaxTokenDuration:   24 * time.Hour,
		LoginTokenDuration: 15 * time.Minute,
	})
	_, token, err := machine.Exchange(t.Context(), TestingGithubProvider(t.Context(), "github-token-user2"))
	if err != nil {
		t.Fatalf("unexpected error during exchange: %v", err)
	}

	t.Run("granted org 1 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 admin, got: %v", err)
		}
	})

	t.Run("granted org 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 read, got: %v", err)
		}
	})

	t.Run("granted workspace 2 write via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   2,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 2 write via org 1, got: %v", err)
		}
	})

	t.Run("denied workspace 3 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   3,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   2,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted app 2 write via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeApp,
			EntityID:   2,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for app 2 write via org 1, got: %v", err)
		}
	})

	t.Run("denied app 3 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeApp,
			EntityID:   3,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 3 has org 1 r, w
func TestUser3Permissions(t *testing.T) {
	machine := tvm.NewVendingMachine(nil, &TestingQueries{tokens: make(map[string]queries.Token)}, tvm.Config{
		MaxTokenDuration:   24 * time.Hour,
		LoginTokenDuration: 15 * time.Minute,
	})
	_, token, err := machine.Exchange(t.Context(), TestingGithubProvider(t.Context(), "github-token-user3"))
	if err != nil {
		t.Fatalf("unexpected error during exchange: %v", err)
	}

	t.Run("granted org 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 read, got: %v", err)
		}
	})

	t.Run("granted org 1 write", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 write, got: %v", err)
		}
	})

	t.Run("denied org 1 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error for org 1 admin, got: %v", err)
		}
	})

	t.Run("granted workspace 1 write via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   1,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 1 write via org 1, got: %v", err)
		}
	})

	t.Run("granted workspace 2 read via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   2,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 2 read via org 1, got: %v", err)
		}
	})

	t.Run("denied workspace 3 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   3,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted app 1 write via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeApp,
			EntityID:   1,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for app 1 write via org 1, got: %v", err)
		}
	})

	t.Run("denied app 3 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeApp,
			EntityID:   3,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   2,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 4 has r or ws 1
func TestUser4Permissions(t *testing.T) {
	machine := tvm.NewVendingMachine(nil, &TestingQueries{tokens: make(map[string]queries.Token)}, tvm.Config{
		MaxTokenDuration:   24 * time.Hour,
		LoginTokenDuration: 15 * time.Minute,
	})
	_, token, err := machine.Exchange(t.Context(), TestingGithubProvider(t.Context(), "github-token-user4"))
	if err != nil {
		t.Fatalf("unexpected error during exchange: %v", err)
	}

	t.Run("granted workspace 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 1 read, got: %v", err)
		}
	})

	t.Run("denied workspace 1 write", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   1,
			Scope:      queries.ScopeWrite,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error for workspace 1 write, got: %v", err)
		}
	})

	t.Run("denied workspace 1 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   1,
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error for workspace 1 admin, got: %v", err)
		}
	})

	t.Run("denied workspace 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   2,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted app 1 read via workspace 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeApp,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for app 1 read via workspace 1, got: %v", err)
		}
	})

	t.Run("denied app 1 write", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeApp,
			EntityID:   1,
			Scope:      queries.ScopeWrite,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error for app 1 write, got: %v", err)
		}
	})

	t.Run("denied app 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeApp,
			EntityID:   2,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 5 has r, w, a of wks 3
func TestUser5Permissions(t *testing.T) {
	machine := tvm.NewVendingMachine(nil, &TestingQueries{tokens: make(map[string]queries.Token)}, tvm.Config{
		MaxTokenDuration:   24 * time.Hour,
		LoginTokenDuration: 15 * time.Minute,
	})
	_, token, err := machine.Exchange(t.Context(), TestingGithubProvider(t.Context(), "github-token-user5"))
	if err != nil {
		t.Fatalf("unexpected error during exchange: %v", err)
	}

	t.Run("granted workspace 3 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   3,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 read, got: %v", err)
		}
	})

	t.Run("granted workspace 3 write", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   3,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 write, got: %v", err)
		}
	})

	t.Run("granted workspace 3 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   3,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 admin, got: %v", err)
		}
	})

	t.Run("denied workspace 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   2,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   2,
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   1,
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted app 3 read via workspace 3", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeApp,
			EntityID:   3,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for app 3 read via workspace 3, got: %v", err)
		}
	})

	t.Run("granted app 3 write via workspace 3", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeApp,
			EntityID:   3,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for app 3 write via workspace 3, got: %v", err)
		}
	})

	t.Run("granted app 3 admin via workspace 3", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeApp,
			EntityID:   3,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for app 3 admin via workspace 3, got: %v", err)
		}
	})

	t.Run("denied app 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeApp,
			EntityID:   1,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}
