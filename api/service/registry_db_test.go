package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/buildsource"
	"github.com/team-loco/loco/api/pkg/imagepath"
	"github.com/team-loco/loco/api/pkg/registrytoken"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	registryv1 "github.com/team-loco/loco/gen/go/loco/registry/v1"
	"github.com/team-loco/loco/gen/go/loco/registry/v1/registryv1connect"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testRegistryService        = "loco-registry"
	testRegistryIssuer         = "https://loco.test"
	testRegistryAccessLifetime = 5 * time.Minute
	testRegistryPullLifetime   = 24 * time.Hour
	testRegistryClockSkew      = 5 * time.Second
	registryChallenge          = `Basic realm="` + testRegistryService + `"`
	testRegistryKeyID          = "loco-registry:1"
)

var registryExchanges = []string{http.MethodGet, http.MethodPost}

type registryTestSigner struct {
	key ed25519.PrivateKey
}

func (s registryTestSigner) Sign(_ context.Context, _ string, input []byte) ([]byte, error) {
	return ed25519.Sign(s.key, input), nil
}

type registryFixture struct {
	*deployFixture
	server      *RegistryServer
	client      registryv1connect.RegistryServiceClient
	public      ed25519.PublicKey
	workspaceID uuid.UUID
}

func testRegistryConfig() RegistryConfig {
	return RegistryConfig{
		TokenMode:              true,
		Service:                testRegistryService,
		Issuer:                 testRegistryIssuer,
		RegistryHost:           testRegistryHost,
		RegistryPrefix:         testRegistryPrefix,
		AccessTokenLifetime:    testRegistryAccessLifetime,
		PullCredentialLifetime: testRegistryPullLifetime,
	}
}

func newRegistryFixture(t *testing.T, config RegistryConfig) *registryFixture {
	t.Helper()
	f := newDeployFixture(t)
	return registryFixtureFor(t, f, config)
}

func registryFixtureFor(t *testing.T, f *deployFixture, config RegistryConfig) *registryFixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clusters := NewClusterAuthenticator(f.queries, testAgentConfig().AssertionMaxLifetime)
	signer := registryTestSigner{key: private}
	server := NewRegistryServer(f.queries, clusters, signer, testRegistryKeyID, config)
	rpcServer := connect.NewServer()
	registryv1connect.RegisterRegistryServiceHandler(rpcServer, server)
	transport := connectinprocess.New(rpcServer)
	client := registryv1connect.NewRegistryServiceClient(connect.NewClient(transport))
	var workspaceID uuid.UUID
	row := f.pool.QueryRow(t.Context(), "SELECT workspace_id FROM resources WHERE id = $1", f.resourceID)
	if scanErr := row.Scan(&workspaceID); scanErr != nil {
		t.Fatal(scanErr)
	}
	return &registryFixture{
		deployFixture: f,
		server:        server,
		client:        client,
		public:        public,
		workspaceID:   workspaceID,
	}
}

func (f *registryFixture) openWindow(t *testing.T, clusterID uuid.UUID) {
	t.Helper()
	params := genDb.OpenPlacementWindowParams{ClusterID: clusterID, ResourceID: f.resourceID}
	if err := f.queries.OpenPlacementWindow(t.Context(), params); err != nil {
		t.Fatal(err)
	}
}

func (f *registryFixture) closeWindow(t *testing.T, clusterID uuid.UUID) {
	t.Helper()
	params := genDb.ClosePlacementWindowParams{ResourceID: f.resourceID, ClusterID: clusterID}
	if err := f.queries.ClosePlacementWindow(t.Context(), params); err != nil {
		t.Fatal(err)
	}
}

func (f *registryFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func (f *registryFixture) issue(
	t *testing.T,
	clusterID uuid.UUID,
	workspaceID string,
) (*registryv1.IssuePullCredentialResponse, error) {
	t.Helper()
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+testAssertion(t, clusterID))
	request := &registryv1.IssuePullCredentialRequest{WorkspaceId: workspaceID}
	return f.client.IssuePullCredential(ctx, request)
}

func (f *registryFixture) pullCredential(t *testing.T) string {
	t.Helper()
	resp, err := f.issue(t, f.clusterID, f.workspaceID.String())
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetPassword()
}

func workspaceRepository(workspace, resource string) string {
	return testRegistryPrefix + "/" + imagepath.WorkspaceDirPrefix + workspace + "/" + resource
}

type registryExchange struct {
	method   string
	username string
	password string
	service  string
	scopes   []string
	values   url.Values
}

func (f *registryFixture) exchange(t *testing.T, e registryExchange) *httptest.ResponseRecorder {
	t.Helper()
	values := url.Values{"service": {e.service}, "scope": e.scopes}
	if e.method == http.MethodPost {
		values.Set("grant_type", registryGrantTypePassword)
		values.Set("client_id", "containerd-client")
		values.Set("username", e.username)
		values.Set("password", e.password)
	}
	maps.Copy(values, e.values)
	var request *http.Request
	if e.method == http.MethodPost {
		body := strings.NewReader(values.Encode())
		request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, registrytoken.TokenPath, body)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	} else {
		target := registrytoken.TokenPath + "?" + values.Encode()
		request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		if e.username != "" || e.password != "" {
			request.SetBasicAuth(e.username, e.password)
		}
	}
	recorder := httptest.NewRecorder()
	f.server.Token(recorder, request)
	return recorder
}

func pullExchange(method, password string, scopes ...string) registryExchange {
	return registryExchange{
		method:   method,
		username: locoControllerV1.RegistryPullUsername,
		password: password,
		service:  testRegistryService,
		scopes:   scopes,
	}
}

func buildExchange(method, password string, scopes ...string) registryExchange {
	e := pullExchange(method, password, scopes...)
	e.username = locoControllerV1.RegistryBuildUsername
	return e
}

func (f *registryFixture) servedPublicKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	handler, err := NewRegistryKeyHandler(f.public)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, registrytoken.KeyPath, nil)
	handler.ServeHTTP(recorder, request)
	block, _ := pem.Decode(recorder.Body.Bytes())
	if block == nil {
		t.Fatalf("key endpoint = %q", recorder.Body.String())
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		t.Fatalf("served key is a %T", parsed)
	}
	return key
}

type issuedRegistryToken struct {
	claims registrytoken.Claims
	raw    map[string]json.RawMessage
	body   map[string]json.RawMessage
}

func (f *registryFixture) granted(t *testing.T, e registryExchange) issuedRegistryToken {
	t.Helper()
	recorder := f.exchange(t, e)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s exchange = %d %q, want 200", e.method, recorder.Code, recorder.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var token string
	if err := json.Unmarshal(body["access_token"], &token); err != nil {
		t.Fatalf("access_token in %s: %v", recorder.Body.String(), err)
	}
	issued := f.verify(t, token)
	issued.body = body
	return issued
}

func (f *registryFixture) verify(t *testing.T, token string) issuedRegistryToken {
	t.Helper()
	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		t.Fatal(err)
	}
	if kid := parsed.Headers[0].KeyID; kid != testRegistryKeyID {
		t.Fatalf("kid = %q, want %q", kid, testRegistryKeyID)
	}
	var issued issuedRegistryToken
	public := f.servedPublicKey(t)
	if err := parsed.Claims(public, &issued.claims, &issued.raw); err != nil {
		t.Fatalf("token does not verify with the served PEM key: %v", err)
	}
	return issued
}

func (f *registryFixture) access(t *testing.T, e registryExchange) []registrytoken.Access {
	t.Helper()
	return f.granted(t, e).claims.Access
}

func wantRegistryStatus(t *testing.T, recorder *httptest.ResponseRecorder, want int, what string) {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("%s: status %d %q, want %d", what, recorder.Code, recorder.Body.String(), want)
	}
	if want != http.StatusUnauthorized {
		return
	}
	if challenge := recorder.Header().Get("WWW-Authenticate"); challenge != registryChallenge {
		t.Fatalf("%s: WWW-Authenticate = %q, want %q", what, challenge, registryChallenge)
	}
}

func pullAccess(name string) []registrytoken.Access {
	return []registrytoken.Access{
		{Type: registrytoken.TypeRepository, Name: name, Actions: []string{registrytoken.ActionPull}},
	}
}

func TestIssuePullCredentialRequiresAnOpenPlacementWindow(t *testing.T) {
	f := newRegistryFixture(t, testRegistryConfig())
	workspace := f.workspaceID.String()

	_, err := f.issue(t, f.clusterID, workspace)
	requireCode(t, err, connect.CodePermissionDenied, "workspace without a placement window")

	f.openWindow(t, f.clusterID)
	_, err = f.issue(t, f.otherCluster, workspace)
	requireCode(t, err, connect.CodePermissionDenied, "workspace placed on another cluster")
	_, err = f.issue(t, f.clusterID, uuid.NewString())
	requireCode(t, err, connect.CodePermissionDenied, "another workspace")
	_, err = f.issue(t, f.clusterID, "not-a-uuid")
	requireCode(t, err, connect.CodeInvalidArgument, "malformed workspace id")

	before := time.Now()
	resp, err := f.issue(t, f.clusterID, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetUsername() != locoControllerV1.RegistryPullUsername || resp.GetPassword() == "" {
		t.Fatalf("credential = %q / %q", resp.GetUsername(), resp.GetPassword())
	}
	if resp.GetRegistryHost() != testRegistryHost {
		t.Fatalf("registry host = %q, want %q", resp.GetRegistryHost(), testRegistryHost)
	}
	lifetime := resp.GetExpiresAt().AsTime().Sub(before)
	if lifetime < testRegistryPullLifetime || lifetime > testRegistryPullLifetime+testRegistryClockSkew {
		t.Fatalf("credential lifetime = %s, want %s", lifetime, testRegistryPullLifetime)
	}
	var (
		kind            string
		storedCluster   uuid.UUID
		storedWorkspace uuid.UUID
		hash            []byte
		expiresAt       time.Time
	)
	row := f.pool.QueryRow(t.Context(),
		"SELECT kind, cluster_id, workspace_id, secret_hash, expires_at FROM registry_credentials")
	if scanErr := row.Scan(&kind, &storedCluster, &storedWorkspace, &hash, &expiresAt); scanErr != nil {
		t.Fatal(scanErr)
	}
	sum := sha256.Sum256([]byte(resp.GetPassword()))
	if kind != registryCredentialPull || storedCluster != f.clusterID || storedWorkspace != f.workspaceID {
		t.Fatalf("stored credential = %s %s %s", kind, storedCluster, storedWorkspace)
	}
	if !reflect.DeepEqual(hash, sum[:]) {
		t.Fatalf("stored hash = %x, want the SHA-256 of the password", hash)
	}
	if !expiresAt.Equal(resp.GetExpiresAt().AsTime().Truncate(time.Microsecond)) {
		t.Fatalf("stored expiry = %s, want %s", expiresAt, resp.GetExpiresAt().AsTime())
	}

	f.closeWindow(t, f.clusterID)
	_, err = f.issue(t, f.clusterID, workspace)
	requireCode(t, err, connect.CodePermissionDenied, "closed placement window")

	f.openWindow(t, f.clusterID)
	f.exec(t, "UPDATE clusters SET removed_at = NOW() WHERE id = $1", f.clusterID)
	_, err = f.issue(t, f.clusterID, workspace)
	requireCode(t, err, connect.CodeUnauthenticated, "removed cluster")
}

func TestIssuePullCredentialFailsInStaticMode(t *testing.T) {
	config := testRegistryConfig()
	config.TokenMode = false
	f := newRegistryFixture(t, config)
	f.openWindow(t, f.clusterID)
	_, err := f.issue(t, f.clusterID, f.workspaceID.String())
	requireCode(t, err, connect.CodeFailedPrecondition, "static mode")
}

func TestRegistryRealmGrantsPullOnTheWorkspaceRepositoriesOnly(t *testing.T) {
	f := newRegistryFixture(t, testRegistryConfig())
	f.openWindow(t, f.clusterID)
	password := f.pullCredential(t)
	workspace := f.workspaceID.String()
	repository := workspaceRepository(workspace, "r")
	nested := workspaceRepository(workspace, "r/cache")
	other := uuid.NewString()
	refused := []string{
		"repository:" + workspaceRepository(workspace+"x", "r") + ":pull",
		"repository:" + testRegistryPrefix + "/" + imagepath.WorkspaceDirPrefix + workspace + ":pull",
		"repository:" + workspaceRepository(workspace, "../"+imagepath.WorkspaceDirPrefix+other+"/r") + ":pull",
		"repository:" + workspaceRepository(other, "r") + ":pull",
		"repository:other/" + imagepath.WorkspaceDirPrefix + workspace + "/r:pull",
		"repository:" + imagepath.WorkspaceDirPrefix + workspace + "/r:pull",
		"repository::pull",
		"repository:" + strings.ToUpper(repository) + ":pull",
		"repository:" + repository + "/:pull",
		"repository:" + workspaceRepository(workspace, "r2") + ":push",
		"registry:catalog:*",
	}
	for _, method := range registryExchanges {
		t.Run(method, func(t *testing.T) {
			scopes := append([]string{"repository:" + repository + ":pull,push,delete"}, refused...)
			scopes = append(scopes, "repository:"+nested+":pull")
			issued := f.granted(t, pullExchange(method, password, scopes...))
			want := append(pullAccess(repository), pullAccess(nested)...)
			if !reflect.DeepEqual(issued.claims.Access, want) {
				t.Fatalf("access = %+v, want %+v", issued.claims.Access, want)
			}
			claims := issued.claims
			if claims.Subject != f.clusterID.String() || claims.Issuer != testRegistryIssuer || claims.ID == "" {
				t.Fatalf("claims = %+v", claims)
			}
			if aud := string(issued.raw["aud"]); aud != `"`+testRegistryService+`"` {
				t.Fatalf("aud = %s", aud)
			}
			if lifetime := claims.Expiry.Time().Sub(claims.IssuedAt.Time()); lifetime != testRegistryAccessLifetime {
				t.Fatalf("exp - iat = %s, want %s", lifetime, testRegistryAccessLifetime)
			}
			if *claims.NotBefore != *claims.IssuedAt {
				t.Fatalf("nbf = %v, iat = %v", claims.NotBefore, claims.IssuedAt)
			}
			if _, ok := issued.raw["loco.build"]; ok {
				t.Fatalf("pull token carries loco.build: %v", issued.raw)
			}
			wantResponseFields(t, method, issued.body)

			empty := f.access(t, pullExchange(method, password, refused...))
			if len(empty) != 0 {
				t.Fatalf("access for refused scopes = %+v, want none", empty)
			}
		})
	}
}

func TestRegistryAPITokenCarriesOnlyTheRequestedAccess(t *testing.T) {
	f := newRegistryFixture(t, testRegistryConfig())
	catalog := pullAccess(registrytoken.CatalogRepository)
	token, err := f.server.SignAPIToken(t.Context(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	issued := f.verify(t, token)
	claims := issued.claims
	if !reflect.DeepEqual(claims.Access, catalog) {
		t.Fatalf("access = %+v, want %+v", claims.Access, catalog)
	}
	if name := string(issued.raw["access"]); !strings.Contains(name, `"name":""`) {
		t.Fatalf("access = %s, want the empty catalog repository name", name)
	}
	if claims.Subject != RegistryAPISubject || claims.Issuer != testRegistryIssuer || claims.ID == "" {
		t.Fatalf("claims = %+v", claims)
	}
	if aud := string(issued.raw["aud"]); aud != `"`+testRegistryService+`"` {
		t.Fatalf("aud = %s", aud)
	}
	if lifetime := claims.Expiry.Time().Sub(claims.IssuedAt.Time()); lifetime != testRegistryAccessLifetime {
		t.Fatalf("exp - iat = %s, want %s", lifetime, testRegistryAccessLifetime)
	}
	if _, ok := issued.raw["loco.build"]; ok {
		t.Fatalf("API token carries loco.build: %v", issued.raw)
	}
}

func wantResponseFields(t *testing.T, method string, body map[string]json.RawMessage) {
	t.Helper()
	if string(body["expires_in"]) != "300" {
		t.Fatalf("expires_in = %s, want 300", body["expires_in"])
	}
	if _, ok := body["issued_at"]; !ok {
		t.Fatalf("response %v lacks issued_at", body)
	}
	if method == http.MethodPost {
		if string(body["token_type"]) != `"Bearer"` {
			t.Fatalf("token_type = %s", body["token_type"])
		}
		return
	}
	if string(body["token"]) != string(body["access_token"]) {
		t.Fatalf("token = %s, access_token = %s", body["token"], body["access_token"])
	}
}

func TestRegistryRealmRefusesInvalidCredentials(t *testing.T) {
	f := newRegistryFixture(t, testRegistryConfig())
	f.openWindow(t, f.clusterID)
	password := f.pullCredential(t)
	expired := f.pullCredential(t)
	expiredHash := sha256.Sum256([]byte(expired))
	f.exec(t, "UPDATE registry_credentials SET expires_at = NOW() - INTERVAL '1 second' WHERE secret_hash = $1",
		expiredHash[:])
	scope := "repository:" + workspaceRepository(f.workspaceID.String(), "r") + ":pull"
	for _, method := range registryExchanges {
		t.Run(method, func(t *testing.T) {
			issued := f.granted(t, pullExchange(method, password, scope))
			var accessToken string
			if err := json.Unmarshal(issued.body["access_token"], &accessToken); err != nil {
				t.Fatal(err)
			}
			cases := map[string]registryExchange{
				"unknown credential":       pullExchange(method, "not-a-credential", scope),
				"expired credential":       pullExchange(method, expired, scope),
				"access token as password": pullExchange(method, accessToken, scope),
				"no credential":            pullExchange(method, "", scope),
			}
			asBuild := pullExchange(method, password, scope)
			asBuild.username = locoControllerV1.RegistryBuildUsername
			cases["pull credential as loco-build"] = asBuild
			unknownUser := pullExchange(method, password, scope)
			unknownUser.username = "loco"
			cases["unknown username"] = unknownUser
			for name, exchange := range cases {
				wantRegistryStatus(t, f.exchange(t, exchange), http.StatusUnauthorized, name)
			}
		})
	}
}

func TestRegistryRealmRejectsMalformedRequests(t *testing.T) {
	f := newRegistryFixture(t, testRegistryConfig())
	f.openWindow(t, f.clusterID)
	password := f.pullCredential(t)
	scope := "repository:" + workspaceRepository(f.workspaceID.String(), "r") + ":pull"
	for _, method := range registryExchanges {
		t.Run(method, func(t *testing.T) {
			wrongService := pullExchange(method, password, scope)
			wrongService.service = "other-registry"
			noService := pullExchange(method, password, scope)
			noService.service = ""
			cases := map[string]registryExchange{
				"wrong service":   wrongService,
				"no service":      noService,
				"malformed scope": pullExchange(method, password, "repository:"+f.workspaceID.String()),
				"scope without actions": pullExchange(
					method,
					password,
					"repository:"+workspaceRepository(f.workspaceID.String(), "r")+":",
				),
			}
			if method == http.MethodPost {
				refresh := pullExchange(method, password, scope)
				refresh.values = url.Values{"grant_type": {"refresh_token"}}
				cases["refresh_token grant"] = refresh
			}
			for name, exchange := range cases {
				wantRegistryStatus(t, f.exchange(t, exchange), http.StatusBadRequest, name)
			}
		})
	}
}

func TestRegistryPullCredentialStopsWhenThePlacementEnds(t *testing.T) {
	f := newRegistryFixture(t, testRegistryConfig())
	f.openWindow(t, f.clusterID)
	first := f.pullCredential(t)
	second := f.pullCredential(t)
	repository := workspaceRepository(f.workspaceID.String(), "r")
	scope := "repository:" + repository + ":pull"
	for _, password := range []string{first, second} {
		for _, method := range registryExchanges {
			if access := f.access(t, pullExchange(method, password, scope)); !reflect.DeepEqual(
				access,
				pullAccess(repository),
			) {
				t.Fatalf("%s access with one of two credentials = %+v", method, access)
			}
		}
	}

	f.closeWindow(t, f.clusterID)
	for _, method := range registryExchanges {
		recorder := f.exchange(t, pullExchange(method, first, scope))
		wantRegistryStatus(t, recorder, http.StatusUnauthorized, method+" after the window closed")
	}

	f.openWindow(t, f.clusterID)
	f.granted(t, pullExchange(http.MethodGet, first, scope))
	f.exec(t, "UPDATE clusters SET removed_at = NOW() WHERE id = $1", f.clusterID)
	for _, method := range registryExchanges {
		recorder := f.exchange(t, pullExchange(method, first, scope))
		wantRegistryStatus(t, recorder, http.StatusUnauthorized, method+" after the cluster was removed")
	}
}

func (f *registryFixture) insertBuild(t *testing.T, status genDb.BuildStatus) (uuid.UUID, string) {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	repository := imagepath.Repository(testRegistryHost, testRegistryPrefix, f.workspaceID, f.resourceID)
	f.exec(t, `
INSERT INTO builds (id, resource_id, cluster_id, status, source_type, source_key, source_size,
                    dockerfile_path, image_repository, created_by)
VALUES ($1, $2, $3, $4, 'upload', $5, 10, 'Dockerfile', $6, $7)`,
		id, f.resourceID, f.clusterID, status, buildsource.Key(id), repository, uuid.New(),
	)
	path, err := imagepath.Path(testRegistryHost, repository)
	if err != nil {
		t.Fatal(err)
	}
	return id, path
}

func (f *registryFixture) insertBuildCredential(t *testing.T, buildID uuid.UUID) string {
	t.Helper()
	password := uuid.NewString()
	hash := sha256.Sum256([]byte(password))
	f.exec(t, `
INSERT INTO registry_credentials (kind, cluster_id, workspace_id, build_id, secret_hash)
VALUES ('build', $1, $2, $3, $4)`,
		f.clusterID, f.workspaceID, buildID, hash[:],
	)
	return password
}

func TestRegistryBuildCredentialGrantsItsBuildRepositoryWhileTheBuildRuns(t *testing.T) {
	f := newRegistryFixture(t, testRegistryConfig())
	buildID, repository := f.insertBuild(t, genDb.BuildStatusQueued)
	password := f.insertBuildCredential(t, buildID)
	sibling := workspaceRepository(f.workspaceID.String(), uuid.NewString())
	build := func(method string, scopes ...string) registryExchange {
		return buildExchange(method, password, scopes...)
	}
	want := []registrytoken.Access{{
		Type:    registrytoken.TypeRepository,
		Name:    repository,
		Actions: []string{registrytoken.ActionPull, registrytoken.ActionPush},
	}}
	ownScope := "repository:" + repository + ":push,pull," + registrytoken.ActionDelete
	for _, status := range []genDb.BuildStatus{genDb.BuildStatusQueued, genDb.BuildStatusRunning} {
		f.exec(t, "UPDATE builds SET status = $2 WHERE id = $1", buildID, status)
		for _, method := range registryExchanges {
			issued := f.granted(t, build(method, ownScope, "repository:"+sibling+":pull,push"))
			if !reflect.DeepEqual(issued.claims.Access, want) {
				t.Fatalf("%s %s access = %+v, want %+v", status, method, issued.claims.Access, want)
			}
			if issued.claims.Build != buildID.String() {
				t.Fatalf("loco.build = %q, want %s", issued.claims.Build, buildID)
			}
			if access := f.access(t, build(method, "repository:"+sibling+":pull")); len(access) != 0 {
				t.Fatalf("access to another repository = %+v", access)
			}
			asPull := pullExchange(method, password, ownScope)
			wantRegistryStatus(t, f.exchange(t, asPull), http.StatusUnauthorized, "build credential as loco-pull")
		}
	}

	f.exec(t, "UPDATE builds SET cluster_id = $2 WHERE id = $1", buildID, f.otherCluster)
	for _, method := range registryExchanges {
		wantRegistryStatus(t, f.exchange(t, build(method, ownScope)), http.StatusUnauthorized, "build moved cluster")
	}
	f.exec(t, "UPDATE builds SET cluster_id = $2 WHERE id = $1", buildID, f.clusterID)
	f.granted(t, build(http.MethodGet, ownScope))
	f.exec(t, "UPDATE builds SET status = 'failed', finished_at = NOW() WHERE id = $1", buildID)
	for _, method := range registryExchanges {
		wantRegistryStatus(t, f.exchange(t, build(method, ownScope)), http.StatusUnauthorized, "finished build")
	}
}

type registryCredentialSet struct {
	pull       string
	build      string
	otherPull  string
	pullScope  string
	buildScope string
}

func (f *registryFixture) issueCredentialSet(t *testing.T) registryCredentialSet {
	t.Helper()
	f.openWindow(t, f.clusterID)
	f.openWindow(t, f.otherCluster)
	buildID, repository := f.insertBuild(t, genDb.BuildStatusRunning)
	other, err := f.issue(t, f.otherCluster, f.workspaceID.String())
	if err != nil {
		t.Fatal(err)
	}
	set := registryCredentialSet{
		pull:       f.pullCredential(t),
		build:      f.insertBuildCredential(t, buildID),
		otherPull:  other.GetPassword(),
		pullScope:  "repository:" + workspaceRepository(f.workspaceID.String(), "r") + ":pull",
		buildScope: "repository:" + repository + ":pull,push",
	}
	f.wantCredentialSet(t, set, http.StatusOK, "before the cluster changed")
	return set
}

func (f *registryFixture) wantCredentialSet(t *testing.T, set registryCredentialSet, want int, what string) {
	t.Helper()
	pull := pullExchange(http.MethodGet, set.pull, set.pullScope)
	wantRegistryStatus(t, f.exchange(t, pull), want, "pull credential "+what)
	build := pullExchange(http.MethodGet, set.build, set.buildScope)
	build.username = locoControllerV1.RegistryBuildUsername
	wantRegistryStatus(t, f.exchange(t, build), want, "build credential "+what)
	other := pullExchange(http.MethodGet, set.otherPull, set.pullScope)
	wantRegistryStatus(t, f.exchange(t, other), http.StatusOK, "other cluster's pull credential "+what)
}

func TestRegistryCredentialsEndWhenTheClusterReenrolls(t *testing.T) {
	f := newRegistryFixture(t, testRegistryConfig())
	set := f.issueCredentialSet(t)
	admin := NewClusterAdminServer(f.pool, f.queries, testAgentConfig().JoinTokenTTL)
	token, err := admin.IssueJoinToken(
		systemAdminContext(t),
		&clusterv1.IssueJoinTokenRequest{ClusterId: f.clusterID.String()},
	)
	if err != nil {
		t.Fatal(err)
	}
	agent := startSyncServer(t, f.deployFixture)
	enrollment := &agentv1.Enrollment{
		JoinToken: token.GetJoinToken().GetToken(),
		PublicKey: publicKeyBytes(t, newAgentKey(t)),
	}
	request := &agentv1.RegisterRequest{ClusterKey: f.clusterID.String(), Enrollment: enrollment}
	if _, err := agent.Register(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	f.wantCredentialSet(t, set, http.StatusUnauthorized, "after the cluster re-enrolled")
}

func TestRegistryCredentialsStayRevokedWhenARemovedClusterIsRestored(t *testing.T) {
	f := newRegistryFixture(t, testRegistryConfig())
	set := f.issueCredentialSet(t)
	for _, operation := range []clusterOperation{clusterPause, clusterRemove, clusterRestore} {
		if _, err := manageCluster(t.Context(), f.pool, operation, f.clusterID, true); err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
	}
	f.wantCredentialSet(t, set, http.StatusUnauthorized, "after the cluster was removed and restored")
}
