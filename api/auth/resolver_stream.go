package auth

import (
	"context"
	"fmt"

	genDb "github.com/team-loco/loco/api/gen/db"
)

// LookupExisting reads a linked user without provisioning, touching identities,
// claiming invitations or granting bootstrap administrator access.
func (r *Resolver) LookupExisting(ctx context.Context, identity Identity) (genDb.User, error) {
	row, err := r.queries.GetUserByIdentity(ctx, genDb.GetUserByIdentityParams{
		Issuer: identity.Issuer, Subject: identity.Subject,
	})
	if err != nil {
		return genDb.User{}, fmt.Errorf("read existing stream user: %w", err)
	}
	return row.User, nil
}
