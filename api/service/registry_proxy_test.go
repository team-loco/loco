package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/team-loco/loco/api/gen/db"
)

func TestRegistryProxyScopesUploadAndKeepsUpstreamCredentialsOnServer(t *testing.T) {
	f := newInfrastructureFixture(t)
	environment, err := f.deployment.queries.GetEnvironmentByID(f.ctx, f.deployment.envID)
	if err != nil {
		t.Fatal(err)
	}
	token := "loco_k_" + uuid.NewString()
	if err = f.deployment.queries.CreateAPIToken(f.ctx, db.CreateAPITokenParams{
		ID:         uuid.New(),
		TokenHash:  hashToken(token),
		Name:       "publisher",
		EntityType: db.EntityTypeEnvironment,
		EntityID:   environment.ID,
		CreatedBy:  environment.CreatedBy,
		ExpiresAt:  time.Now().Add(time.Hour),
		StackName:  infraTestStack,
		Scopes: []db.EntityScope{
			{EntityType: db.EntityTypeEnvironment, EntityID: environment.ID, Scope: db.ScopeWrite},
		},
	}); err != nil {
		t.Fatal(err)
	}
	physical := "project/images/" + environment.ID.String() + "/storefront.api"
	virtual := "/v2/loco/" + environment.ID.String() + "/storefront/api/blobs/uploads/"
	var upstream *httptest.Server
	var uploads atomic.Int32
	upstream = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/":
			w.Header().
				Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="registry-test"`, upstream.URL))
			w.WriteHeader(http.StatusUnauthorized)
		case "/api/v4/user":
			if r.Header.Get("PRIVATE-TOKEN") != "test-server-credential" {
				t.Error("missing server-side GitLab identity credential")
			}
			fmt.Fprint(w, `{"username":"registry-robot"}`)
		case "/token":
			username, password, authenticated := r.BasicAuth()
			if !authenticated || username != "registry-robot" || password != "test-server-credential" {
				t.Error("invalid server-side token exchange")
			}
			if r.URL.Query().Get("scope") != "repository:"+physical+":pull,push" {
				t.Error("registry exchange requested the wrong repository")
			}
			fmt.Fprint(w, `{"token":"upstream-test-bearer"}`)
		default:
			if r.URL.Path != "/v2/"+physical+"/blobs/uploads/" {
				t.Errorf("wrong upstream repository: %s", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer upstream-test-bearer" || r.Header.Get("Cookie") != "" {
				t.Error("client credentials reached the upstream registry")
			}
			if r.URL.Query().Get("mount") != "" || r.URL.Query().Get("from") != "" {
				t.Error("cross-repository mount was forwarded")
			}
			uploads.Add(1)
			w.Header().Set("Location", upstream.URL+"/v2/"+physical+"/blobs/uploads/upload-id?_state=opaque")
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer upstream.Close()
	server := NewRegistryServer(
		f.deployment.pool,
		f.deployment.queries,
		f.server.machine,
		upstream.URL,
		"test-server-credential",
		"project",
		strings.TrimPrefix(upstream.URL, "https://")+"/project/images",
		upstream.Client(),
	)
	request := httptest.NewRequest(http.MethodPost, virtual+"?mount=foreign&from=another-repository", nil)
	request.SetBasicAuth("loco", token)
	request.Header.Set("Cookie", "private=value")
	response := httptest.NewRecorder()
	server.ProxyHandler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("upload failed: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Location") != virtual+"upload-id?_state=opaque" {
		t.Fatalf("upload location was not rewritten: %s", response.Header().Get("Location"))
	}
	if strings.Contains(response.Body.String()+fmt.Sprint(response.Header()), "test-server-credential") {
		t.Fatal("upstream credential leaked")
	}
	foreign := httptest.NewRequest(http.MethodPost, strings.Replace(virtual, "/storefront/", "/foreign/", 1), nil)
	foreign.SetBasicAuth("loco", token)
	rejected := httptest.NewRecorder()
	server.ProxyHandler().ServeHTTP(rejected, foreign)
	if rejected.Code != http.StatusForbidden || uploads.Load() != 1 {
		t.Fatal("stack credential reached a foreign registry repository")
	}
	if err = f.server.machine.Revoke(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	revoked := httptest.NewRecorder()
	server.ProxyHandler().ServeHTTP(revoked, request)
	if revoked.Code != http.StatusUnauthorized || uploads.Load() != 1 {
		t.Fatal("revoked credential reused a cached upstream token")
	}
}
