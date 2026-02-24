package tvm_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/providers"
)

type TestingQueries struct {
	// here's the database:
	// org 1, workspace 1, 2, resource 1 in ws 1, resource 2 in ws 2
	// org 2, workspace 3, resource 3 in ws 3
	// user 1 user1@loco-testing.com: has no scopes
	// user 2 user2@loco-testing.com: r, w, a of org 1
	// user 3 user3@loco-testing.com: r, w of org 1
	// user 4 user4@loco-testing.com: r or ws 1
	// user 5 user5@loco-testing.com: r, w, a of wks 3
	queries.Querier
	tokens map[string]queries.Token
}

var (
	user1UUID = uuid.MustParse("01890000-0000-0000-0000-000000000001")
	user2UUID = uuid.MustParse("01890000-0000-0000-0000-000000000002")
	user3UUID = uuid.MustParse("01890000-0000-0000-0000-000000000003")
	user4UUID = uuid.MustParse("01890000-0000-0000-0000-000000000004")
	user5UUID = uuid.MustParse("01890000-0000-0000-0000-000000000005")
	org1UUID  = uuid.MustParse("01890000-0000-0000-0000-000000000011")
	org2UUID  = uuid.MustParse("01890000-0000-0000-0000-000000000012")
	ws1UUID   = uuid.MustParse("01890000-0000-0000-0000-000000000021")
	ws2UUID   = uuid.MustParse("01890000-0000-0000-0000-000000000022")
	ws3UUID   = uuid.MustParse("01890000-0000-0000-0000-000000000023")
	res1UUID  = uuid.MustParse("01890000-0000-0000-0000-000000000031")
	res2UUID  = uuid.MustParse("01890000-0000-0000-0000-000000000032")
	res3UUID  = uuid.MustParse("01890000-0000-0000-0000-000000000033")
)

func (*TestingQueries) GetUserByEmail(ctx context.Context, email string) (queries.User, error) {
	switch email {
	case "user1@loco-testing.com":
		return queries.User{ID: user1UUID, Email: email}, nil
	case "user2@loco-testing.com":
		return queries.User{ID: user2UUID, Email: email}, nil
	case "user3@loco-testing.com":
		return queries.User{ID: user3UUID, Email: email}, nil
	case "user4@loco-testing.com":
		return queries.User{ID: user4UUID, Email: email}, nil
	case "user5@loco-testing.com":
		return queries.User{ID: user5UUID, Email: email}, nil
	default:
		return queries.User{}, tvm.ErrUserNotFound
	}
}

func (*TestingQueries) GetUserScopes(ctx context.Context, userID uuid.UUID) ([]queries.EntityScope, error) {
	switch userID {
	case user1UUID:
		return []queries.EntityScope{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user1UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user1UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user1UUID},
		}, nil
	case user2UUID:
		return []queries.EntityScope{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user2UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user2UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user2UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
		}, nil
	case user3UUID:
		return []queries.EntityScope{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user3UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user3UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user3UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
		}, nil
	case user4UUID:
		return []queries.EntityScope{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user4UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user4UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user4UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: ws1UUID},
		}, nil
	case user5UUID:
		return []queries.EntityScope{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user5UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user5UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user5UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: ws3UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeWorkspace, EntityID: ws3UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeWorkspace, EntityID: ws3UUID},
		}, nil
	default:
		return nil, tvm.ErrUserNotFound
	}
}

func (tq *TestingQueries) GetUserScopesByEmail(ctx context.Context, email string) ([]queries.EntityScope, error) {
	switch email {
	case "user1@loco-testing.com":
		return tq.GetUserScopes(ctx, user1UUID)
	case "user2@loco-testing.com":
		return tq.GetUserScopes(ctx, user2UUID)
	case "user3@loco-testing.com":
		return tq.GetUserScopes(ctx, user3UUID)
	case "user4@loco-testing.com":
		return tq.GetUserScopes(ctx, user4UUID)
	case "user5@loco-testing.com":
		return tq.GetUserScopes(ctx, user5UUID)
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

func (*TestingQueries) GetOrganizationIDByWorkspaceID(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	if id == ws1UUID || id == ws2UUID {
		return org1UUID, nil
	}
	if id == ws3UUID {
		return org2UUID, nil
	}
	return uuid.UUID{}, tvm.ErrEntityNotFound
}

func (*TestingQueries) GetWorkspaceOrganizationIDByResourceID(ctx context.Context, id uuid.UUID) (queries.GetWorkspaceOrganizationIDByResourceIDRow, error) {
	if id == res1UUID {
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: ws1UUID,
			OrgID:       org1UUID,
		}, nil
	}
	if id == res2UUID {
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: ws2UUID,
			OrgID:       org1UUID,
		}, nil
	}
	if id == res3UUID {
		return queries.GetWorkspaceOrganizationIDByResourceIDRow{
			WorkspaceID: ws3UUID,
			OrgID:       org2UUID,
		}, nil
	}
	return queries.GetWorkspaceOrganizationIDByResourceIDRow{}, tvm.ErrEntityNotFound
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
		return providers.NewEmailResponse("user1@loco-testing.com", nil)
	case "github-token-user2":
		return providers.NewEmailResponse("user2@loco-testing.com", nil)
	case "github-token-user3":
		return providers.NewEmailResponse("user3@loco-testing.com", nil)
	case "github-token-user4":
		return providers.NewEmailResponse("user4@loco-testing.com", nil)
	case "github-token-user5":
		return providers.NewEmailResponse("user5@loco-testing.com", nil)
	}
	return providers.NewEmailResponse("", tvm.ErrUserNotFound)
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
			EntityID:   org1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied workspace 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted self read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   user1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for self read, got: %v", err)
		}
	})

	t.Run("denied other user read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeUser,
			EntityID:   user2UUID,
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
			EntityID: org1UUID,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 admin, got: %v", err)
		}
	})

	t.Run("granted org 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID: org1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 read, got: %v", err)
		}
	})

	t.Run("granted workspace 2 write via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID: ws2UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 2 write via org 1, got: %v", err)
		}
	})

	t.Run("denied workspace 3 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID: ws3UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID: org2UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted resource 2 write via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID: res2UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for resource 2 write via org 1, got: %v", err)
		}
	})

	t.Run("denied resource 3 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID: res3UUID,
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
			EntityID:   org1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 read, got: %v", err)
		}
	})

	t.Run("granted org 1 write", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 write, got: %v", err)
		}
	})

	t.Run("denied org 1 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error for org 1 admin, got: %v", err)
		}
	})

	t.Run("granted workspace 1 write via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 1 write via org 1, got: %v", err)
		}
	})

	t.Run("granted workspace 2 read via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws2UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 2 read via org 1, got: %v", err)
		}
	})

	t.Run("denied workspace 3 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws3UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted resource 1 write via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res1UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for resource 1 write via org 1, got: %v", err)
		}
	})

	t.Run("denied resource 3 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res3UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org2UUID,
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
			EntityID:   ws1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 1 read, got: %v", err)
		}
	})

	t.Run("denied workspace 1 write", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error for workspace 1 write, got: %v", err)
		}
	})

	t.Run("denied workspace 1 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error for workspace 1 admin, got: %v", err)
		}
	})

	t.Run("denied workspace 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws2UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted resource 1 read via workspace 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for resource 1 read via workspace 1, got: %v", err)
		}
	})

	t.Run("denied resource 1 write", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res1UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error for resource 1 write, got: %v", err)
		}
	})

	t.Run("denied resource 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res2UUID,
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
			EntityID:   ws3UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 read, got: %v", err)
		}
	})

	t.Run("granted workspace 3 write", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws3UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 write, got: %v", err)
		}
	})

	t.Run("granted workspace 3 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws3UUID,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 3 admin, got: %v", err)
		}
	})

	t.Run("denied workspace 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org2UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org2UUID,
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeAdmin,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted resource 3 read via workspace 3", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res3UUID,
			Scope:      queries.ScopeRead,
		})
		if err != nil {
			t.Errorf("expected no error for resource 3 read via workspace 3, got: %v", err)
		}
	})

	t.Run("granted resource 3 write via workspace 3", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res3UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for resource 3 write via workspace 3, got: %v", err)
		}
	})

	t.Run("granted resource 3 admin via workspace 3", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res3UUID,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for resource 3 admin via workspace 3, got: %v", err)
		}
	})

	t.Run("denied resource 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res1UUID,
			Scope:      queries.ScopeRead,
		})
		if err != tvm.ErrInsufficentPermissions {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}
