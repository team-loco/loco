package interceptor

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/gen/go/loco/oauth/v1/oauthv1connect"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
)

func TestAuthenticateSkipsPublicProcedures(t *testing.T) {
	i := NewGithubAuthInterceptor(nil)
	ctx, err := i.authenticate(t.Context(), oauthv1connect.OAuthServiceRefreshTokenProcedure, http.Header{})
	if err != nil {
		t.Fatalf("authenticate public procedure: %v", err)
	}
	if ctx != t.Context() {
		t.Fatal("authenticate changed the context of a public procedure")
	}
}

func TestAuthenticateRejectsMissingToken(t *testing.T) {
	i := NewGithubAuthInterceptor(nil)
	_, err := i.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, http.Header{})
	if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", code)
	}
}
