package tvm

import (
	"context"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	queries "github.com/team-loco/loco/api/gen/db"
)

func (tvm *VendingMachine) StackRestriction(ctx context.Context, token string) (*contextkeys.StackRestriction, error) {
	if tokenPrefix(token) != prefixAPIKey {
		return nil, nil
	}
	row, err := tvm.queries.GetAPIToken(ctx, hashToken(token))
	if err != nil {
		return nil, err
	}
	if row.StackName == "" {
		return nil, nil
	}
	if row.EntityType != queries.EntityTypeEnvironment {
		return nil, ErrInsufficentPermissions
	}
	return &contextkeys.StackRestriction{EnvironmentID: row.EntityID, Name: row.StackName}, nil
}

func VerifyStackTarget(ctx context.Context, environmentID uuid.UUID, name string) error {
	restriction, ok := ctx.Value(contextkeys.StackRestrictionKey).(*contextkeys.StackRestriction)
	if ok && restriction != nil && (restriction.EnvironmentID != environmentID || restriction.Name != name) {
		return ErrInsufficentPermissions
	}
	return nil
}

func IsStackRestricted(ctx context.Context) bool {
	restriction, ok := ctx.Value(contextkeys.StackRestrictionKey).(*contextkeys.StackRestriction)
	return ok && restriction != nil
}
