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
	request := &tokenv1.CreateTokenRequest{
		Name:         name,
		EntityType:   tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		EntityId:     w.ID,
		ExpiresInSec: apiTokenTTLSeconds,
	}
	for _, scope := range scopes {
		request.Scopes = append(
			request.Scopes,
			&tokenv1.EntityScope{Scope: scope, EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, EntityId: w.ID},
		)
	}
	response, err := w.p.User.Token.CreateToken(tb.Context(), request)
	if err != nil {
		tb.Fatalf("create token %s: %v", name, err)
	}
	return response.GetToken()
}

// Clients calls the API with token.
func (p *Platform) Clients(token string) *rpc.Clients {
	return rpc.ForHost(p.APIURL, token)
}
