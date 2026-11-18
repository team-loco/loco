// Package authztest holds a fixed table of users and their scopes for tests of code that
// reads user scopes.
package authztest

import (
	"errors"

	"github.com/google/uuid"

	queries "github.com/team-loco/loco/api/gen/db"
)

// errUnknownUser is UserScopes' answer for a user outside the table.
var errUnknownUser = errors.New("unknown test user")

var (
	User1      = uuid.MustParse("01890000-0000-0000-0000-000000000001")
	User2      = uuid.MustParse("01890000-0000-0000-0000-000000000002")
	User3      = uuid.MustParse("01890000-0000-0000-0000-000000000003")
	User4      = uuid.MustParse("01890000-0000-0000-0000-000000000004")
	User5      = uuid.MustParse("01890000-0000-0000-0000-000000000005")
	Org1       = uuid.MustParse("01890000-0000-0000-0000-000000000011")
	Workspace1 = uuid.MustParse("01890000-0000-0000-0000-000000000021")
	Workspace3 = uuid.MustParse("01890000-0000-0000-0000-000000000023")
)

// UserScopes returns the scopes the table grants userID: User1 holds only its own user scopes,
// User2 administers Org1, User3 writes Org1, User4 reads Workspace1 and User5 administers
// Workspace3.
func UserScopes(userID uuid.UUID) ([]queries.GetUserScopesRow, error) {
	switch userID {
	case User1:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: User1},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: User1},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: User1},
		}, nil
	case User2:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: User2},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: User2},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: User2},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: Org1},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: Org1},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeOrganization, EntityID: Org1},
		}, nil
	case User3:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: User3},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: User3},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: User3},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeOrganization, EntityID: Org1},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeOrganization, EntityID: Org1},
		}, nil
	case User4:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: User4},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: User4},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: User4},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: Workspace1},
		}, nil
	case User5:
		return []queries.GetUserScopesRow{
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeUser, EntityID: User5},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeUser, EntityID: User5},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeUser, EntityID: User5},
			{Scope: queries.ScopeRead, EntityType: queries.EntityTypeWorkspace, EntityID: Workspace3},
			{Scope: queries.ScopeWrite, EntityType: queries.EntityTypeWorkspace, EntityID: Workspace3},
			{Scope: queries.ScopeAdmin, EntityType: queries.EntityTypeWorkspace, EntityID: Workspace3},
		}, nil
	default:
		return nil, errUnknownUser
	}
}
