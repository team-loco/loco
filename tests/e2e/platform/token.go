package platform

import (
	"testing"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/rpc"
)

const apiTokenTTLSeconds = 3600

// MintToken creates a workspace API token with scopes as the application user.
func (w *Workspace) MintToken(tb testing.TB, name string, scopes ...tokenv1.Scope) string {
	tb.Helper()
	workspace := tokenv1.EntityType_ENTITY_TYPE_WORKSPACE
	return CreateToken(tb, w.p.User, name, workspace, w.ID, Scopes(workspace, w.ID, scopes...)...)
}

// CreateToken creates an API token owned by ownerID with clients.
func CreateToken(tb testing.TB, clients *rpc.Clients, name string, owner tokenv1.EntityType, ownerID string,
	scopes ...*tokenv1.EntityScope,
) string {
	tb.Helper()
	response, err := clients.Token.CreateToken(tb.Context(), TokenRequest(name, owner, ownerID, scopes...))
	if err != nil {
		tb.Fatalf("create token %s: %v", name, err)
	}
	return response.GetToken()
}

// TokenRequest asks for a one-hour API token owned by ownerID.
func TokenRequest(name string, owner tokenv1.EntityType, ownerID string,
	scopes ...*tokenv1.EntityScope,
) *tokenv1.CreateTokenRequest {
	return &tokenv1.CreateTokenRequest{
		Name: name, EntityType: owner, EntityId: ownerID, Scopes: scopes, ExpiresInSec: apiTokenTTLSeconds,
	}
}

// Scopes grants each scope on one entity.
func Scopes(entity tokenv1.EntityType, id string, scopes ...tokenv1.Scope) []*tokenv1.EntityScope {
	granted := make([]*tokenv1.EntityScope, 0, len(scopes))
	for _, scope := range scopes {
		granted = append(granted, &tokenv1.EntityScope{EntityType: entity, EntityId: id, Scope: scope})
	}
	return granted
}

// Clients calls the API with token.
func (p *Platform) Clients(token string) *rpc.Clients {
	return rpc.ForHost(p.APIURL, token)
}
