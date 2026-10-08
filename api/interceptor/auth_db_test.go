package interceptor

import (
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
)

type providerFixture struct {
	pool        *pgxpool.Pool
	queries     *genDb.Queries
	machine     *tvm.VendingMachine
	issuer      *authtest.Issuer
	interceptor *authInterceptor
}

func newProviderFixture(t *testing.T, policy auth.SignupPolicy) *providerFixture {
	t.Helper()
	pool := authtest.NewPool(t)
	issuer := authtest.NewIssuer(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Minute,
		MaxAPITokenDuration:         time.Hour,
	})
	t.Cleanup(machine.Close)
	verifier := auth.NewVerifier(http.DefaultClient, []auth.IssuerConfig{{
		Issuer:   issuer.URL(),
		JWKSURL:  issuer.URL() + "/.well-known/jwks.json",
		Audience: "authenticated",
		Claims: auth.ClaimPaths{
			Subject:       "sub",
			Email:         "email",
			EmailVerified: "email_verified",
		},
	}})
	return &providerFixture{
		pool:        pool,
		queries:     queries,
		machine:     machine,
		issuer:      issuer,
		interceptor: NewAuthInterceptor(machine, verifier, auth.NewResolver(pool, policy), auth.NewSSOGate(queries)),
	}
}

func bearer(token string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	return h
}

func TestAuthenticateProviderTokenProvisionsUser(t *testing.T) {
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := newProviderFixture(t, policy)
	token := f.issuer.Sign("k1", f.issuer.Claims("sub-1", "dev@example.test", true))

	ctx, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(token))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok || entity.Type != genDb.EntityTypeUser {
		t.Fatalf("entity = %+v", entity)
	}
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok || len(scopes) != 3 {
		t.Fatalf("scopes = %+v", scopes)
	}
	identity, ok := ctx.Value(contextkeys.IdentityKey).(auth.Identity)
	if !ok || identity.Subject != "sub-1" {
		t.Fatalf("identity = %+v", identity)
	}

	again, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(token))
	if err != nil {
		t.Fatalf("second authenticate: %v", err)
	}
	againEntity, ok := again.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok || againEntity.ID != entity.ID {
		t.Fatalf("second request resolved to %+v, want %s", againEntity, entity.ID)
	}
}

func TestAuthenticateProviderTokenErrors(t *testing.T) {
	policy, err := auth.ParseSignupPolicy("domains", "acme.test")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := newProviderFixture(t, policy)
	other := authtest.NewIssuer(t)

	cases := []struct {
		name  string
		token string
		want  connect.Code
	}{
		{
			"rejected by signup policy",
			f.issuer.Sign("k1", f.issuer.Claims("s", "dev@evil.test", true)),
			connect.CodePermissionDenied,
		},
		{"untrusted issuer", other.Sign("k1", other.Claims("s", "dev@acme.test", true)), connect.CodeUnauthenticated},
		{"unknown loco token", "loco_s_doesnotexist", connect.CodeUnauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(tc.token))
			if code := connect.CodeOf(err); code != tc.want {
				t.Fatalf("code = %v (%v), want %v", code, err, tc.want)
			}
		})
	}
}
