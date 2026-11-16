package apps

import (
	"testing"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

func TestTokenDisplayFragments(t *testing.T) {
	t.Parallel()
	tenant := p.NewTenant(t)
	secret := tenant.MintToken(t, "display", tokenv1.Scope_SCOPE_READ)
	request := &tokenv1.GetTokenRequest{
		Name:       "display",
		EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		EntityId:   tenant.ID,
	}
	response, err := p.User.Token.GetToken(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := p.User.Token.ListTokens(
		t.Context(),
		&tokenv1.ListTokensRequest{EntityType: request.GetEntityType(), EntityId: tenant.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, metadata := range append(listed.GetTokens(), response.GetToken()) {
		if metadata.GetDisplayPrefix() != secret[:11] || metadata.GetDisplaySuffix() != secret[len(secret)-4:] {
			t.Fatalf("display metadata = %v", metadata)
		}
	}
}
