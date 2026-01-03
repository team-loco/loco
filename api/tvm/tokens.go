package tvm

import (
	"context"
	"log/slog"

	queries "github.com/team-loco/loco/api/gen/db"
)

func (tvm *VendingMachine) GetToken(ctx context.Context, token string) (queries.Entity, []queries.EntityScope, error) {
	tokenData, err := tvm.queries.GetToken(ctx, token)
	if err != nil {
		slog.ErrorContext(ctx, err.Error())
		return queries.Entity{}, nil, err
	}

	return queries.Entity{
		Type: tokenData.EntityType,
		ID:   tokenData.EntityID,
	}, tokenData.Scopes, nil
}

// Revoke deletes the given token, effectively immediately revoking it.
func (tvm *VendingMachine) Revoke(ctx context.Context, token string) error {
	return tvm.queries.DeleteToken(ctx, token)
}

// ListTokensForEntity lists all tokens associated with the given entity. This function does not check the permissions of the caller.
// It is expected that the caller has already verified that the caller has sufficient permissions to list the tokens for the given entity.
func (tvm *VendingMachine) ListTokensForEntity(ctx context.Context, entity queries.Entity) ([]queries.ListTokensForEntityRow, error) {
	return tvm.queries.ListTokensForEntity(ctx, queries.ListTokensForEntityParams{
		EntityType: entity.Type,
		EntityID:   entity.ID,
	})
}
