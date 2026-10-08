package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
)

type SSOGate struct {
	queries genDb.Querier
}

func NewSSOGate(queries genDb.Querier) *SSOGate {
	return &SSOGate{queries: queries}
}

func (g *SSOGate) Filter(
	ctx context.Context,
	scopes []genDb.EntityScope,
	connection *string,
) ([]genDb.EntityScope, error) {
	types := make([]string, 0, len(scopes))
	ids := make([]uuid.UUID, 0, len(scopes))
	for _, s := range scopes {
		if s.EntityType == genDb.EntityTypeSystem || s.EntityType == genDb.EntityTypeUser {
			continue
		}
		types = append(types, string(s.EntityType))
		ids = append(ids, s.EntityID)
	}
	if len(ids) == 0 {
		return scopes, nil
	}
	gated, err := g.queries.ListSSOGatedEntities(ctx, genDb.ListSSOGatedEntitiesParams{
		EntityTypes: types,
		EntityIds:   ids,
	})
	if err != nil {
		return nil, fmt.Errorf("list sso gated entities: %w", err)
	}
	if len(gated) == 0 {
		return scopes, nil
	}
	blocked := make(map[uuid.UUID]struct{}, len(gated))
	for _, row := range gated {
		if connection == nil || *connection != row.ConnectionID {
			blocked[row.EntityID] = struct{}{}
		}
	}
	if len(blocked) == 0 {
		return scopes, nil
	}
	out := make([]genDb.EntityScope, 0, len(scopes))
	for _, s := range scopes {
		if _, ok := blocked[s.EntityID]; ok && s.EntityType != genDb.EntityTypeSystem {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}
