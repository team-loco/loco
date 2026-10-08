package tvm_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
)

// sessionEntry is the in-memory representation of a session_token row.
type sessionEntry struct {
	id               uuid.UUID
	userID           uuid.UUID
	accessHash       string
	refreshHash      string
	accessExpiresAt  time.Time
	refreshExpiresAt time.Time
	lastUsedAt       time.Time
}

// TestingQueries is an in-memory implementation of queries.Querier for unit tests.
// It supports the session and user-scope operations exercised by the TVM permission tests.
type TestingQueries struct {
	queries.Querier
	sessions  map[uuid.UUID]*sessionEntry
	byAccess  map[string]uuid.UUID
	byRefresh map[string]uuid.UUID
}

func newTestingQueries() *TestingQueries {
	return &TestingQueries{
		sessions:  make(map[uuid.UUID]*sessionEntry),
		byAccess:  make(map[string]uuid.UUID),
		byRefresh: make(map[string]uuid.UUID),
	}
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

func (*TestingQueries) GetUserScopes(_ context.Context, userID uuid.UUID) ([]queries.GetUserScopesRow, error) {
	switch userID {
	case user1UUID:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user1UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user1UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user1UUID},
		}, nil
	case user2UUID:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user2UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user2UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user2UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
		}, nil
	case user3UUID:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user3UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user3UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user3UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: org1UUID},
		}, nil
	case user4UUID:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user4UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user4UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user4UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: ws1UUID},
		}, nil
	case user5UUID:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: user5UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: user5UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: user5UUID},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: ws3UUID},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeWorkspace, EntityID: ws3UUID},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeWorkspace, EntityID: ws3UUID},
		}, nil
	default:
		return nil, errUnknownUser
	}
}

func (*TestingQueries) GetUserScopesOnWorkspace(
	_ context.Context,
	_ queries.GetUserScopesOnWorkspaceParams,
) ([]queries.GetUserScopesOnWorkspaceRow, error) {
	return nil, nil
}

func (*TestingQueries) GetOrganizationIDByWorkspaceID(_ context.Context, id uuid.UUID) (uuid.UUID, error) {
	if id == ws1UUID || id == ws2UUID {
		return org1UUID, nil
	}
	if id == ws3UUID {
		return org2UUID, nil
	}
	return uuid.UUID{}, tvm.ErrEntityNotFound
}

func (*TestingQueries) GetWorkspaceOrganizationIDByResourceID(
	_ context.Context,
	id uuid.UUID,
) (queries.GetWorkspaceOrganizationIDByResourceIDRow, error) {
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

// --- Session token mock implementations ---

func (tq *TestingQueries) CreateSessionToken(_ context.Context, params queries.CreateSessionTokenParams) error {
	entry := &sessionEntry{
		id:               params.ID,
		userID:           params.UserID,
		accessHash:       params.AccessTokenHash,
		refreshHash:      params.RefreshTokenHash,
		accessExpiresAt:  params.AccessExpiresAt,
		refreshExpiresAt: params.RefreshExpiresAt,
		lastUsedAt:       time.Now(),
	}
	tq.sessions[params.ID] = entry
	tq.byAccess[params.AccessTokenHash] = params.ID
	tq.byRefresh[params.RefreshTokenHash] = params.ID
	return nil
}

func (tq *TestingQueries) GetSessionWithScopesByAccessToken(
	ctx context.Context,
	accessTokenHash string,
) (queries.GetSessionWithScopesByAccessTokenRow, error) {
	id, ok := tq.byAccess[accessTokenHash]
	if !ok {
		return queries.GetSessionWithScopesByAccessTokenRow{}, tvm.ErrTokenNotFound
	}
	e := tq.sessions[id]
	rows, err := tq.GetUserScopes(ctx, e.userID)
	if err != nil {
		return queries.GetSessionWithScopesByAccessTokenRow{}, err
	}
	scopes := make([]queries.EntityScope, len(rows))
	for i, row := range rows {
		scopes[i] = queries.EntityScope(row)
	}
	encoded, err := json.Marshal(scopes)
	if err != nil {
		return queries.GetSessionWithScopesByAccessTokenRow{}, err
	}
	return queries.GetSessionWithScopesByAccessTokenRow{
		ID:         e.id,
		UserID:     e.userID,
		LastUsedAt: e.lastUsedAt,
		Scopes:     encoded,
	}, nil
}

func (tq *TestingQueries) GetSessionByRefreshToken(
	_ context.Context,
	refreshTokenHash string,
) (queries.GetSessionByRefreshTokenRow, error) {
	id, ok := tq.byRefresh[refreshTokenHash]
	if !ok {
		return queries.GetSessionByRefreshTokenRow{}, tvm.ErrTokenNotFound
	}
	e := tq.sessions[id]
	return queries.GetSessionByRefreshTokenRow{
		ID:               e.id,
		UserID:           e.userID,
		RefreshTokenHash: e.refreshHash,
		AccessExpiresAt:  e.accessExpiresAt,
		RefreshExpiresAt: e.refreshExpiresAt,
		LastUsedAt:       e.lastUsedAt,
	}, nil
}

func (tq *TestingQueries) RotateSessionToken(
	_ context.Context,
	params queries.RotateSessionTokenParams,
) (int64, error) {
	e, ok := tq.sessions[params.ID]
	if !ok || e.refreshHash != params.OldRefreshTokenHash {
		return 0, nil
	}
	delete(tq.byAccess, e.accessHash)
	delete(tq.byRefresh, e.refreshHash)
	e.accessHash = params.AccessTokenHash
	e.refreshHash = params.RefreshTokenHash
	e.accessExpiresAt = params.AccessExpiresAt
	e.refreshExpiresAt = params.RefreshExpiresAt
	tq.byAccess[e.accessHash] = e.id
	tq.byRefresh[e.refreshHash] = e.id
	return 1, nil
}

func (*TestingQueries) TouchSessionLastUsed(_ context.Context, _ uuid.UUID) error { return nil }

func (tq *TestingQueries) DeleteSessionToken(_ context.Context, id uuid.UUID) error {
	e, ok := tq.sessions[id]
	if !ok {
		return nil
	}
	delete(tq.byAccess, e.accessHash)
	delete(tq.byRefresh, e.refreshHash)
	delete(tq.sessions, id)
	return nil
}

func (tq *TestingQueries) DeleteSessionTokenByAccessHash(ctx context.Context, accessTokenHash string) error {
	id, ok := tq.byAccess[accessTokenHash]
	if !ok {
		return nil
	}
	return tq.DeleteSessionToken(ctx, id)
}

func (*TestingQueries) DeleteExpiredSessionTokens(_ context.Context) error { return nil }

func (*TestingQueries) ListSessionsForUser(
	_ context.Context,
	_ uuid.UUID,
) ([]queries.ListSessionsForUserRow, error) {
	return nil, nil
}

// --- API token mock implementations (no-op; not exercised by permission tests) ---

func (*TestingQueries) CreateAPIToken(_ context.Context, _ queries.CreateAPITokenParams) error {
	return nil
}

func (*TestingQueries) GetAPIToken(_ context.Context, _ string) (queries.GetAPITokenRow, error) {
	return queries.GetAPITokenRow{}, tvm.ErrTokenNotFound
}

func (*TestingQueries) TouchAPITokenLastUsed(_ context.Context, _ uuid.UUID) error { return nil }

func (*TestingQueries) DeleteAPIToken(_ context.Context, _ uuid.UUID) error { return nil }

func (*TestingQueries) DeleteAPITokenByHash(_ context.Context, _ string) error { return nil }

func (*TestingQueries) DeleteExpiredAPITokens(_ context.Context) error { return nil }

func (*TestingQueries) ListAPITokensForEntity(
	_ context.Context,
	_ queries.ListAPITokensForEntityParams,
) ([]queries.ListAPITokensForEntityRow, error) {
	return nil, nil
}

func (*TestingQueries) DeleteAPITokensForEntity(_ context.Context, _ queries.DeleteAPITokensForEntityParams) error {
	return nil
}

func (*TestingQueries) GetAPITokenByNameAndEntity(
	_ context.Context,
	_ queries.GetAPITokenByNameAndEntityParams,
) (queries.GetAPITokenByNameAndEntityRow, error) {
	return queries.GetAPITokenByNameAndEntityRow{}, tvm.ErrTokenNotFound
}

func (*TestingQueries) DeleteAPITokenByNameAndEntity(
	_ context.Context,
	_ queries.DeleteAPITokenByNameAndEntityParams,
) error {
	return nil
}

// --- Test helpers ---

var errUnknownUser = errors.New("unknown test user")

func session(t *testing.T, machine *tvm.VendingMachine, userID uuid.UUID) (string, string) {
	t.Helper()
	access, refresh, err := machine.IssueSession(t.Context(), userID, nil, "", "")
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	return access, refresh
}

func testConfig() tvm.Config {
	return tvm.Config{
		MaxAPITokenDuration:         24 * time.Hour,
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: 24 * time.Hour,
		LastUsedUpdateInterval:      5 * time.Minute,
	}
}

// --- Permission tests ---
// user 1 has only self read/write/admin
func TestUser1Permissions(t *testing.T) {
	machine := tvm.NewVendingMachine(nil, newTestingQueries(), testConfig())
	token, _ := session(t, machine, user1UUID)

	t.Run("denied org 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied workspace 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
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
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 2 has org 1 r, w, a
func TestUser2Permissions(t *testing.T) {
	machine := tvm.NewVendingMachine(nil, newTestingQueries(), testConfig())
	token, _ := session(t, machine, user2UUID)

	t.Run("granted org 1 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil {
			t.Errorf("expected no error for org 1 admin, got: %v", err)
		}
	})

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

	t.Run("granted workspace 2 write via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws2UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for workspace 2 write via org 1, got: %v", err)
		}
	})

	t.Run("denied workspace 3 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws3UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org2UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("granted resource 2 write via org 1", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res2UUID,
			Scope:      queries.ScopeWrite,
		})
		if err != nil {
			t.Errorf("expected no error for resource 2 write via org 1, got: %v", err)
		}
	})

	t.Run("denied resource 3 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res3UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 3 has org 1 r, w
func TestUser3Permissions(t *testing.T) {
	machine := tvm.NewVendingMachine(nil, newTestingQueries(), testConfig())
	token, _ := session(t, machine, user3UUID)

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
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
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
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
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
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org2UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 4 has r of ws 1
func TestUser4Permissions(t *testing.T) {
	machine := tvm.NewVendingMachine(nil, newTestingQueries(), testConfig())
	token, _ := session(t, machine, user4UUID)

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
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error for workspace 1 write, got: %v", err)
		}
	})

	t.Run("denied workspace 1 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws1UUID,
			Scope:      queries.ScopeAdmin,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error for workspace 1 admin, got: %v", err)
		}
	})

	t.Run("denied workspace 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeWorkspace,
			EntityID:   ws2UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
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
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error for resource 1 write, got: %v", err)
		}
	})

	t.Run("denied resource 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeResource,
			EntityID:   res2UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

// user 5 has r, w, a of wks 3
func TestUser5Permissions(t *testing.T) {
	machine := tvm.NewVendingMachine(nil, newTestingQueries(), testConfig())
	token, _ := session(t, machine, user5UUID)

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
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 read", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org2UUID,
			Scope:      queries.ScopeRead,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 2 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org2UUID,
			Scope:      queries.ScopeAdmin,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})

	t.Run("denied org 1 admin", func(t *testing.T) {
		err := machine.Verify(context.Background(), token, queries.EntityScope{
			EntityType: queries.EntityTypeOrganization,
			EntityID:   org1UUID,
			Scope:      queries.ScopeAdmin,
		})
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
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
		if !errors.Is(err, tvm.ErrInsufficentPermissions) {
			t.Errorf("expected insufficient permissions error, got: %v", err)
		}
	})
}

type racingRefreshQueries struct {
	*TestingQueries
}

func (rq racingRefreshQueries) RotateSessionToken(
	ctx context.Context,
	params queries.RotateSessionTokenParams,
) (int64, error) {
	competing := params
	competing.AccessTokenHash = "competing-access"
	competing.RefreshTokenHash = "competing-refresh"
	if _, err := rq.TestingQueries.RotateSessionToken(ctx, competing); err != nil {
		return 0, err
	}
	return rq.TestingQueries.RotateSessionToken(ctx, params)
}

func TestRefreshRotatesTokens(t *testing.T) {
	machine := tvm.NewVendingMachine(nil, newTestingQueries(), testConfig())
	_, refreshToken := session(t, machine, user1UUID)

	access, newRefresh, err := machine.Refresh(t.Context(), refreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if access == "" || newRefresh == "" || newRefresh == refreshToken {
		t.Fatalf("refresh did not issue a new token pair")
	}

	if _, _, err := machine.Refresh(t.Context(), refreshToken); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("reusing a rotated refresh token: got %v, want ErrInvalidExpiredToken", err)
	}
}

func TestRefreshLosingRaceRevokesSession(t *testing.T) {
	tq := newTestingQueries()
	machine := tvm.NewVendingMachine(nil, racingRefreshQueries{tq}, testConfig())
	_, refreshToken := session(t, machine, user1UUID)

	if _, _, err := machine.Refresh(t.Context(), refreshToken); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("losing refresh: got %v, want ErrInvalidExpiredToken", err)
	}
	if len(tq.sessions) != 0 {
		t.Fatalf("sessions = %d, want the session revoked", len(tq.sessions))
	}
}
