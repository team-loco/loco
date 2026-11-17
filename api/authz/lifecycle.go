package authz

import (
	"context"
	"slices"

	"github.com/google/uuid"

	queries "github.com/team-loco/loco/api/gen/db"
)

// LockUsers serializes account mutations before organization and credential locks.
// Multiple accounts are locked in UUID order so actor and target operations cannot invert it.
func LockUsers(ctx context.Context, q queries.Querier, ids ...uuid.UUID) error {
	ids = slices.Clone(ids)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	for _, id := range slices.Compact(ids) {
		if _, err := q.LockUserForMembership(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
