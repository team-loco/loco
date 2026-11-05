package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	bootstrapv1 "github.com/team-loco/loco/gen/go/loco/bootstrap/v1"
	"github.com/team-loco/loco/gen/go/loco/bootstrap/v1/bootstrapv1connect"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	bootstrapContenders         = 8
	bootstrapVerifiedEmailClaim = "email_verified"
)

type bootstrapFixture struct {
	api      *membershipAPI
	issuer   *authtest.Issuer
	endpoint string
}

func newBootstrapFixture(t *testing.T, hash []byte) bootstrapFixture {
	t.Helper()
	a := newMembershipAPI(t)
	issuer := authtest.NewIssuer(t)
	return bootstrapFixture{api: a, issuer: issuer, endpoint: bootstrapHTTP(t, a, issuer, hash)}
}

func bootstrapHTTP(t *testing.T, a *membershipAPI, issuer *authtest.Issuer, hash []byte) string {
	t.Helper()
	issuers, err := auth.ParseIssuers(
		`[{"issuer":"` + issuer.URL() + `","audience":"authenticated","emailVerification":"claim"}]`,
	)
	if err != nil {
		t.Fatal(err)
	}
	verifier := auth.NewVerifier(http.DefaultClient, issuers)
	resolver := auth.NewResolver(a.pool, auth.SignupPolicy{Mode: auth.SignupClosed})
	server := connect.NewServer(interceptor.NewErrorInterceptor())
	sessions := NewAuthServer(a.pool, a.queries, a.machine, nil, auth.Admins{}, "")
	bootstrapv1connect.RegisterBootstrapServiceHandler(
		server,
		NewBootstrapServer(a.pool, hash, verifier, resolver, sessions),
	)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	authenticated := connect.NewServer(
		interceptor.NewErrorInterceptor(),
		interceptor.NewAuthInterceptor(a.machine, verifier, resolver, authz.New(a.pool, a.queries)).Interceptor(),
	)
	userv1connect.RegisterUserServiceHandler(authenticated, NewUserServer(a.pool, a.queries, a.machine, auth.Admins{}))
	connecthttp.Mount(mux, authenticated)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	return endpoint.URL
}

func (f bootstrapFixture) proof(subject string) string {
	return f.issuer.Sign("k1", f.issuer.Claims(subject, subject+"@example.com", true))
}

func bootstrapAs(t *testing.T, endpoint, proof, token string) (*bootstrapv1.BootstrapResponse, error) {
	t.Helper()
	return rpc.NewHTTPClients(
		http.DefaultClient,
		endpoint,
		proof,
	).Bootstrap.Bootstrap(
		t.Context(),
		&bootstrapv1.BootstrapRequest{Token: token},
	)
}

func systemAdmins(t *testing.T, a *membershipAPI) []uuid.UUID {
	t.Helper()
	admins, err := a.queries.GetUsersWithScopeOnEntity(
		t.Context(),
		genDb.GetUsersWithScopeOnEntityParams{
			EntityType: genDb.EntityTypeSystem,
			EntityID:   uuid.Nil,
			Scope:      genDb.ScopeAdmin,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return admins
}

func requireBootstrapError(t *testing.T, err error, code connect.Code, want error) {
	t.Helper()
	ce, ok := errors.AsType[*connect.Error](err)
	if !ok {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if ce.Code() != code || ce.Message() != want.Error() {
		t.Fatalf("error = %s %q, want %s %q", ce.Code(), ce.Message(), code, want)
	}
}

func TestBootstrapFreshClosedInstallation(t *testing.T) {
	f := newBootstrapFixture(t, HashBootstrapToken("installation-proof"))
	proof := f.proof("operator")
	if _, err := rpc.NewHTTPClients(
		http.DefaultClient,
		f.endpoint,
		proof,
	).User.WhoAmI(
		t.Context(),
		&userv1.WhoAmIRequest{},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("ordinary signup before bootstrap = %v", err)
	}
	response, err := bootstrapAs(t, f.endpoint, f.proof("operator"), "installation-proof")
	if err != nil {
		t.Fatal(err)
	}
	if admins := systemAdmins(t, f.api); len(admins) != 1 || admins[0].String() != response.GetUserId() {
		t.Fatalf("admins = %v, user = %s", admins, response.GetUserId())
	}
	if response.GetTokens().GetAccessToken() == "" || response.GetTokens().GetRefreshToken() == "" {
		t.Fatal("missing standard CLI session")
	}
	clusters := rpc.NewHTTPClients(
		http.DefaultClient,
		clusterAdminHTTP(t, f.api.pool, f.api.machine),
		response.GetTokens().GetAccessToken(),
	).ClusterAdmin
	if _, err = clusters.ListClusters(t.Context(), &clusterv1.ListClustersRequest{}); err != nil {
		t.Fatalf("session cannot administer clusters: %v", err)
	}
	closed := auth.NewResolver(f.api.pool, auth.SignupPolicy{Mode: auth.SignupClosed})
	if _, _, err = closed.Resolve(
		t.Context(),
		auth.Identity{Issuer: f.issuer.URL(), Subject: "blocked", Email: "blocked@example.com", EmailVerified: true},
	); err == nil {
		t.Fatal("bootstrap opened ordinary signup")
	}
}

func TestBootstrapRequiresBothProofs(t *testing.T) {
	for _, tc := range []struct {
		name, token     string
		invalidIdentity bool
		code            connect.Code
		want            error
	}{
		{
			name: "installation proof", token: "wrong-installation-proof",
			code: connect.CodePermissionDenied, want: ErrBootstrapTokenInvalid,
		},
		{
			name: "identity proof", token: "installation-proof", invalidIdentity: true,
			code: connect.CodeUnauthenticated, want: ErrBootstrapUserRequired,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBootstrapFixture(t, HashBootstrapToken("installation-proof"))
			proof := f.proof("operator")
			if tc.invalidIdentity {
				proof = ""
			}
			_, err := bootstrapAs(t, f.endpoint, proof, tc.token)
			requireBootstrapError(t, err, tc.code, tc.want)
			var users, closed int
			if err = f.api.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM users),
 (SELECT count(*) FROM system_bootstrap)`).
				Scan(&users, &closed); err != nil {
				t.Fatal(err)
			}
			if users != 0 || closed != 0 {
				t.Fatalf("rejected proof mutated users=%d closure=%d", users, closed)
			}
			if _, err = bootstrapAs(t, f.endpoint, f.proof("operator"), "installation-proof"); err != nil {
				t.Fatalf("rejected attempt consumed bootstrap: %v", err)
			}
		})
	}
}

func TestBootstrapRejectsUntrustedIdentity(t *testing.T) {
	for _, claim := range []string{"iss", "aud", "exp", bootstrapVerifiedEmailClaim} {
		t.Run(claim, func(t *testing.T) {
			f := newBootstrapFixture(t, HashBootstrapToken("installation-proof"))
			claims := f.issuer.Claims("operator", "operator@example.com", true)
			switch claim {
			case "iss":
				claims[claim] = "wrong-issuer"
			case "aud":
				claims[claim] = "wrong-audience"
			case "exp":
				claims[claim] = 1
			case bootstrapVerifiedEmailClaim:
				claims[claim] = false
			}
			_, err := bootstrapAs(t, f.endpoint, f.issuer.Sign("k1", claims), "installation-proof")
			if claim == bootstrapVerifiedEmailClaim {
				requireBootstrapError(t, err, connect.CodePermissionDenied, auth.ErrEmailUnverified)
			} else {
				requireBootstrapError(t, err, connect.CodeUnauthenticated, ErrBootstrapUserRequired)
			}
			if _, err = bootstrapAs(t, f.endpoint, f.proof("operator"), "installation-proof"); err != nil {
				t.Fatalf("failed verification closed bootstrap: %v", err)
			}
		})
	}
}

func TestBootstrapIsDisabledWithoutToken(t *testing.T) {
	f := newBootstrapFixture(t, nil)
	_, err := bootstrapAs(t, f.endpoint, f.proof("operator"), "installation-proof")
	requireBootstrapError(t, err, connect.CodeFailedPrecondition, ErrBootstrapDisabled)
}

func TestBootstrapConcurrentAttemptsHaveOneWinner(t *testing.T) {
	f := newBootstrapFixture(t, HashBootstrapToken("installation-proof"))
	proofs := make([]string, bootstrapContenders)
	for i := range proofs {
		proofs[i] = f.proof(uuid.NewString())
	}
	errs := make([]error, bootstrapContenders)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, proof := range proofs {
		wg.Go(func() { <-start; _, errs[i] = bootstrapAs(t, f.endpoint, proof, "installation-proof") })
	}
	close(start)
	wg.Wait()
	winners := 0
	for _, err := range errs {
		if err == nil {
			winners++
		} else {
			requireBootstrapError(t, err, connect.CodeAlreadyExists, ErrBootstrapClosed)
		}
	}
	var users, identities int
	if err := f.api.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM identities)").
		Scan(&users, &identities); err != nil {
		t.Fatal(err)
	}
	if winners != 1 || users != 1 || identities != 1 || len(systemAdmins(t, f.api)) != 1 {
		t.Fatalf("winners=%d users=%d identities=%d", winners, users, identities)
	}
}

func TestBootstrapStaysClosedAfterRestart(t *testing.T) {
	f := newBootstrapFixture(t, HashBootstrapToken("installation-proof"))
	response, err := bootstrapAs(t, f.endpoint, f.proof("operator"), "installation-proof")
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.MustParse(response.GetUserId())
	if err = f.api.queries.RemoveUserScope(
		t.Context(),
		genDb.RemoveUserScopeParams{
			UserID:     user,
			Scope:      genDb.ScopeAdmin,
			EntityType: genDb.EntityTypeSystem,
			EntityID:   uuid.Nil,
		},
	); err != nil {
		t.Fatal(err)
	}
	restarted := bootstrapHTTP(t, f.api, f.issuer, HashBootstrapToken("installation-proof"))
	_, err = bootstrapAs(t, restarted, f.proof("next"), "installation-proof")
	requireBootstrapError(t, err, connect.CodeAlreadyExists, ErrBootstrapClosed)
	if len(systemAdmins(t, f.api)) != 0 {
		t.Fatal("restart reopened bootstrap")
	}
}

func TestBootstrapReturningIdentity(t *testing.T) {
	f := newBootstrapFixture(t, HashBootstrapToken("installation-proof"))
	user, _, err := f.api.resolver.Resolve(
		t.Context(),
		auth.Identity{Issuer: f.issuer.URL(), Subject: "operator", Email: "operator@example.com", EmailVerified: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := bootstrapAs(t, f.endpoint, f.proof("operator"), "installation-proof")
	if err != nil {
		t.Fatal(err)
	}
	if response.GetUserId() != user.ID.String() {
		t.Fatalf("created second identity user: %s", response.GetUserId())
	}
}

func TestBootstrapRefusesWhenAdminExists(t *testing.T) {
	f := newBootstrapFixture(t, HashBootstrapToken("installation-proof"))
	existing := f.api.signUp(t, "existing")
	if err := f.api.queries.AddUserScope(
		t.Context(),
		genDb.AddUserScopeParams{
			UserID:     existing.id,
			Scope:      genDb.ScopeAdmin,
			EntityType: genDb.EntityTypeSystem,
			EntityID:   uuid.Nil,
		},
	); err != nil {
		t.Fatal(err)
	}
	_, err := bootstrapAs(t, f.endpoint, f.proof("operator"), "installation-proof")
	requireBootstrapError(t, err, connect.CodeAlreadyExists, ErrBootstrapClosed)
}

func TestBootstrapRollsBackIdentityWhenGrantFails(t *testing.T) {
	f := newBootstrapFixture(t, HashBootstrapToken("installation-proof"))
	_, err := f.api.pool.Exec(
		t.Context(),
		`CREATE FUNCTION reject_bootstrap_grant() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
 IF NEW.entity_type = 'system' THEN RAISE EXCEPTION 'grant unavailable'; END IF;
 RETURN NEW; END $$;
 CREATE TRIGGER reject_bootstrap_grant BEFORE INSERT ON user_scopes
 FOR EACH ROW EXECUTE FUNCTION reject_bootstrap_grant()`,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = bootstrapAs(t, f.endpoint, f.proof("operator"), "installation-proof")
	requireBootstrapError(t, err, connect.CodeInternal, errBootstrap)
	var users, identities, closed int
	if err = f.api.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM identities),
 (SELECT count(*) FROM system_bootstrap)`).
		Scan(&users, &identities, &closed); err != nil {
		t.Fatal(err)
	}
	if users != 0 || identities != 0 || closed != 0 {
		t.Fatalf("failed grant left users=%d identities=%d closure=%d", users, identities, closed)
	}
	if _, err = f.api.pool.Exec(t.Context(), "DROP TRIGGER reject_bootstrap_grant ON user_scopes"); err != nil {
		t.Fatal(err)
	}
	if _, err = bootstrapAs(t, f.endpoint, f.proof("operator"), "installation-proof"); err != nil {
		t.Fatalf("rollback did not restore bootstrap: %v", err)
	}
}

func TestBootstrapRejectsAccountCollision(t *testing.T) {
	f := newBootstrapFixture(t, HashBootstrapToken("installation-proof"))
	if _, _, err := f.api.resolver.Resolve(
		t.Context(),
		auth.Identity{Issuer: f.issuer.URL(), Subject: "original", Email: "operator@example.com", EmailVerified: true},
	); err != nil {
		t.Fatal(err)
	}
	_, err := bootstrapAs(t, f.endpoint, f.proof("operator"), "installation-proof")
	requireBootstrapError(t, err, connect.CodePermissionDenied, auth.ErrEmailTaken)
	if admins := systemAdmins(t, f.api); len(admins) != 0 {
		t.Fatalf("collision granted admin: %v", admins)
	}
}
