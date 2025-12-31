package tvm

import (
	"context"

	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm/providers"
)

// machine.VerifyWithIdentity(ctx, token, action.New(action.ReadWorkspace, workspaceID)) -> (err error)
// tvm.Exchange(ctx, tvm.Github, githubToken) -> (token string, err error)

// Exchange returns a token for the user with the given email. It is expected that the email has been
// provided by a provider in a trusted manner (e.g. )
func (tvm *VendingMachine) Exchange(ctx context.Context, email providers.Email) (string, error) {
	address, err := email.Address()
	if err != nil {
		return "", ErrExchange
	}

	// look up the user by email
	userScopes, err := tvm.queries.GetUserScopesByEmail(ctx, address)
	if err != nil {
		return "", ErrUserNotFound
	}
	if len(userScopes) == 0 { // either user not found or has no scopes
		return "", ErrUserNotFound
	}
	userID := userScopes[0].UserID

	// issue the token
	return tvm.issueNoCheck(ctx, queries.Entity{
		Type: queries.EntityTypeUser,
		ID:   userID,
	}, queries.EntityScopesFromUserScopes(userScopes), tvm.cfg.LoginTokenDuration)
}
