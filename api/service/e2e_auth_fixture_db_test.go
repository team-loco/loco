package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

func TestE2EAuthFixtureUsesRealProviderAndPublicAPIs(t *testing.T) {
	issuer := os.Getenv("E2E_AUTH_ISSUER")
	if issuer == "" {
		t.Skip("E2E_AUTH_ISSUER requires the component fixture Dex")
	}
	a := newMembershipAPI(t)
	endpoint := componentAuthHTTP(t, a, issuer)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	invoke := func(api string) ([]byte, error) {
		command := exec.CommandContext(t.Context(), "go", "run", "./tools/e2e-auth")
		command.Dir = root
		command.Env = append(
			os.Environ(),
			"E2E_API_URL="+api,
			"E2E_AUTH_ISSUER="+issuer,
			"E2E_AUTH_STATE="+statePath,
		)
		return command.CombinedOutput()
	}
	if output, runErr := invoke(endpoint); runErr != nil {
		t.Fatalf("setup: %v: %s", runErr, output)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		UserID        string `json:"userId"`
		OrgID         string `json:"orgId"`
		WorkspaceID   string `json:"workspaceId"`
		EnvironmentID string `json:"environmentId"`
	}
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.UserID == "" || state.OrgID == "" || state.WorkspaceID == "" || state.EnvironmentID == "" {
		t.Fatal("API-created fixture IDs missing")
	}
	var users, identities, sessions int
	if err = a.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM users),
 (SELECT count(*) FROM identities),
 (SELECT count(*) FROM session_tokens)`).Scan(&users, &identities, &sessions); err != nil {
		t.Fatal(err)
	}
	if users != 2 || identities != 2 || sessions != 2 {
		t.Fatalf("users=%d identities=%d sessions=%d", users, identities, sessions)
	}
	var admins int
	if err = a.pool.QueryRow(t.Context(), `SELECT count(*) FROM user_scopes s JOIN users u ON u.id = s.user_id
 WHERE s.entity_type = 'system' AND s.scope = 'admin' AND u.email = $1`, fixtureAdmin).Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if admins != 1 {
		t.Fatalf("%s holds system admin %d times, want 1", fixtureAdmin, admins)
	}
}

const fixtureAdmin = "loco-admin@test.local"

func componentAuthHTTP(t *testing.T, a *membershipAPI, issuer string) string {
	t.Helper()
	raw, err := json.Marshal(
		[]map[string]string{{"issuer": issuer, "audience": "loco-e2e", "emailVerification": "claim"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	issuers, err := auth.ParseIssuers(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	verifier := auth.NewVerifier(http.DefaultClient, issuers)
	admins := auth.SystemAdmins{fixtureAdmin}
	if err = auth.SyncSystemAdmins(t.Context(), a.pool, admins); err != nil {
		t.Fatal(err)
	}
	resolver := auth.NewResolver(a.pool, auth.SignupPolicy{}, limits.Defaults(), auth.WithSystemAdmins(admins))
	store, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	sessions := NewAuthServer(a.pool, a.queries, a.machine, store, auth.Admins{}, "")
	authenticated := connect.NewServer(
		interceptor.NewErrorInterceptor(),
		interceptor.NewAuthInterceptor(a.machine, verifier, resolver, authz.New(a.pool, a.queries)).Interceptor(),
	)
	authv1connect.RegisterAuthServiceHandler(authenticated, sessions)
	userv1connect.RegisterUserServiceHandler(authenticated, NewUserServer(a.pool, a.queries, a.machine, auth.Admins{}))
	orgv1connect.RegisterOrgServiceHandler(
		authenticated,
		NewOrgServer(a.pool, a.queries, InvitationConfig{}, limits.Defaults()),
	)
	workspacev1connect.RegisterWorkspaceServiceHandler(
		authenticated,
		NewWorkspaceServer(a.pool, a.queries, limits.Defaults()),
	)
	environmentv1connect.RegisterEnvironmentServiceHandler(
		authenticated,
		NewEnvironmentServer(a.pool, a.queries, limits.Defaults()),
	)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, authenticated)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	return endpoint.URL
}
