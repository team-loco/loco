package tvm_test

import (
	"context"
	"testing"
	"time"

	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
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
	tokens map[string]queries.GetTokenRow
}

func (*TestingQueries) GetUserScopes(ctx context.Context, userID int64) ([]queries.UserScope, error) {
	switch userID {
	case 1:
		return []queries.UserScope{
			{UserID: userID, Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: userID},
		}, nil
	case 2:
		return []queries.UserScope{
			{UserID: userID, Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: 1},
			{UserID: userID, Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: 1},
			{UserID: userID, Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeOrganization, EntityID: 1},
		}, nil
	case 3:
		return []queries.UserScope{
			{UserID: userID, Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: 1},
			{UserID: userID, Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: 1},
		}, nil
	case 4:
		return []queries.UserScope{
			{UserID: userID, Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: 1},
		}, nil
	case 5:
		return []queries.UserScope{
			{UserID: userID, Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: userID},
			{UserID: userID, Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: 3},
			{UserID: userID, Scope: queries.ScopeWrite, EntityType: queries.EntityTypeWorkspace, EntityID: 3},
			{UserID: userID, Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeWorkspace, EntityID: 3},
		}, nil
	default:
		return nil, tvm.ErrUserNotFound
	}
}

func (tq *TestingQueries) GetUserScopesByEmail(ctx context.Context, email string) ([]queries.UserScope, error) {
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

func (*TestingQueries) GetWorkspaceOrganizationIDByAppID(ctx context.Context, id int64) (queries.GetWorkspaceOrganizationIDByAppIDRow, error) {
	switch id {
	case 1:
		return queries.GetWorkspaceOrganizationIDByAppIDRow{
			WorkspaceID: 1,
			OrgID:       1,
		}, nil
	case 2:
		return queries.GetWorkspaceOrganizationIDByAppIDRow{
			WorkspaceID: 2,
			OrgID:       1,
		}, nil
	case 3:
		return queries.GetWorkspaceOrganizationIDByAppIDRow{
			WorkspaceID: 3,
			OrgID:       2,
		}, nil
	default:
		return queries.GetWorkspaceOrganizationIDByAppIDRow{}, tvm.ErrEntityNotFound
	}
}

func (tq *TestingQueries) StoreToken(ctx context.Context, params queries.StoreTokenParams) error {
	tq.tokens[params.Token] = queries.GetTokenRow{
		Scopes:     params.Scopes,
		EntityID:   params.EntityID,
		EntityType: params.EntityType,
		ExpiresAt:  params.ExpiresAt,
	}
	return nil
}

func (tq *TestingQueries) GetToken(ctx context.Context, token string) (queries.GetTokenRow, error) {
	tk, ok := tq.tokens[token]
	if !ok {
		return queries.GetTokenRow{}, tvm.ErrTokenNotFound
	}
	return tk, nil
}

func TestingGithubProvider(ctx context.Context, token string) (string, error) {
	switch token {
	case "github-token-user1":
		return "user1@loco-testing.com", nil
	case "github-token-user2":
		return "user2@loco-testing.com", nil
	case "github-token-user3":
		return "user3@loco-testing.com", nil
	case "github-token-user4":
		return "user4@loco-testing.com", nil
	case "github-token-user5":
		return "user5@loco-testing.com", nil
	}
	return "", tvm.ErrUserNotFound
}

func TestExchangeAndVerify(t *testing.T) {
	machine := tvm.NewVendingMachine(&TestingQueries{tokens: make(map[string]queries.GetTokenRow)}, tvm.Config{
		MaxTokenDuration:   24 * time.Hour,
		LoginTokenDuration: 15 * time.Minute,
	})
	token, err := machine.Exchange(t.Context(), TestingGithubProvider, "github-token-user1")
	if err != nil {
		t.Fatalf("unexpected error during exchange: %v", err)
	}

	err = machine.Verify(t.Context(), token, queries.EntityScope{
		Entity: queries.Entity{
			Type: queries.EntityTypeOrganization,
			ID:   1,
		},
		Scope: queries.ScopeRead,
	})
	if err == tvm.ErrInsufficentPermissions {
		t.Log("t1: correctly denied access for user1 (org 1 read by user1)")
	} else if err != nil {
		t.Fatalf("t1: unexpected error during verify: %v", err)
	} else {
		t.Fatalf("t1: expected insufficent permissions error but got no error (uh oh twin ur fried)")
	}

	err = machine.Verify(t.Context(), token, queries.EntityScope{
		Entity: queries.Entity{
			Type: queries.EntityTypeWorkspace,
			ID:   1,
		},
		Scope: queries.ScopeRead,
	})
	if err == tvm.ErrInsufficentPermissions {
		t.Log("t2: correctly denied access for user1 (ws 1 read by user1)")
	} else if err != nil {
		t.Fatalf("t2: unexpected error during verify: %v", err)
	} else {
		t.Fatalf("t2: expected insufficent permissions error but got no error (you, YES YOU, are cooked vrotato chip)")
	}

	err = machine.Verify(t.Context(), token, queries.EntityScope{
		Entity: queries.Entity{
			Type: queries.EntityTypeUser,
			ID:   1,
		},
		Scope: queries.ScopeRead,
	})
	if err == tvm.ErrInsufficentPermissions {
		t.Fatalf("t3: incorrectly denied access: %v", err)
	} else if err != nil {
		t.Fatalf("t3: unexpected error during verify: %v", err)
	} else {
		t.Log("t3: correctly granted access for user1 (user 1 read where user1 has read scope on self)")
	}

	err = machine.Verify(t.Context(), token, queries.EntityScope{
		Entity: queries.Entity{
			Type: queries.EntityTypeUser,
			ID:   2,
		},
		Scope: queries.ScopeRead,
	})
	if err == tvm.ErrInsufficentPermissions {
		t.Log("t4: correctly denied access for user1 (user 2 read by user1)")
	} else if err != nil {
		t.Fatalf("t4: unexpected error during verify: %v", err)
	} else {
		t.Fatalf("t4: expected insufficent permissions error but got no error (you are a baked potato now)")
	}
}
