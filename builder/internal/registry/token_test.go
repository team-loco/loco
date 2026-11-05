package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrregistry "github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

const (
	testUsername      = "loco-build"
	testPassword      = "build-secret"
	testService       = "loco-test-registry"
	testTokenLifetime = 10 * time.Minute
	testRepository    = "ws-a/app"
	imageBlobs        = 4
)

type issuedToken struct {
	at  time.Time
	key int
}

type fakeRegistry struct {
	t        *testing.T
	inner    http.Handler
	host     string
	basic    bool
	mu       sync.Mutex
	clock    time.Time
	key      int
	tokens   map[string]issuedToken
	scope    string
	refuse   int
	realm    string
	body     any
	revoked  bool
	rejected int
	exchange int
	pingFail int
	pings    int
	redirect string
	afterReq func(*fakeRegistry, *http.Request)
}

func newFakeRegistry(t *testing.T, basic bool) *fakeRegistry {
	t.Helper()
	fake := &fakeRegistry{
		t:      t,
		inner:  ggcrregistry.New(),
		basic:  basic,
		clock:  time.Unix(0, 0),
		tokens: map[string]issuedToken{},
		scope:  "repository:" + testRepository + ":" + transport.PushScope,
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse %s: %v", server.URL, err)
	}
	fake.host = u.Host
	fake.realm = "http://" + fake.host + "/token"
	writeDockerConfig(t, fake.host)
	return fake
}

func writeDockerConfig(t *testing.T, host string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	auth := base64.StdEncoding.EncodeToString([]byte(testUsername + ":" + testPassword))
	config := map[string]any{"auths": map[string]any{host: map[string]string{"auth": auth}}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeRegistry) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clock
}

func (f *fakeRegistry) client(t *testing.T, options ...remote.Option) (*Client, *[]time.Duration) {
	t.Helper()
	client := New(t.Context(), true)
	client.now = f.now
	var mu sync.Mutex
	var delays []time.Duration
	client.sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		delays = append(delays, d)
		return nil
	}
	client.options = append(client.options, options...)
	return client, &delays
}

func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/token" {
		f.serveToken(w, r)
		return
	}
	if f.failPing(w, r) || !f.authorized(w, r) {
		return
	}
	if f.redirect != "" && r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blobs/") {
		w.Header().Set("Location", f.redirect+r.URL.Path)
		w.WriteHeader(http.StatusFound)
		return
	}
	f.inner.ServeHTTP(w, r)
	if f.afterReq != nil {
		f.mu.Lock()
		f.afterReq(f, r)
		f.mu.Unlock()
	}
}

func (f *fakeRegistry) failPing(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/v2/" || r.Header.Get("Authorization") != "" {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pings++
	if f.pings > f.pingFail {
		return false
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	return true
}

func (f *fakeRegistry) authorized(w http.ResponseWriter, r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if header == "" {
		if f.basic {
			w.Header().Set("WWW-Authenticate", `Basic realm="loco"`)
		} else {
			challenge := fmt.Sprintf(`Bearer realm=%q,service=%q,scope=%q`, f.realm, testService, f.scope)
			w.Header().Set("WWW-Authenticate", challenge)
		}
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	if f.basic {
		user, password, ok := r.BasicAuth()
		if !ok || user != testUsername || password != testPassword {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	issued, ok := f.tokens[strings.TrimPrefix(header, "Bearer ")]
	if !ok || f.revoked || issued.key != f.key || !f.clock.Before(issued.at.Add(testTokenLifetime)) {
		f.rejected++
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	return true
}

func (f *fakeRegistry) serveToken(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchange++
	user, password, ok := r.BasicAuth()
	query := r.URL.Query()
	if f.refuse != 0 {
		w.WriteHeader(f.refuse)
		return
	}
	if !ok || user != testUsername || password != testPassword {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if query.Get("service") != testService || query.Get("scope") != f.scope {
		f.t.Errorf("token request service=%q scope=%q, want %q %q",
			query.Get("service"), query.Get("scope"), testService, f.scope)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if f.body != nil {
		if err := json.NewEncoder(w).Encode(f.body); err != nil {
			f.t.Errorf("encode token body: %v", err)
		}
		return
	}
	token := fmt.Sprintf("token-%d", f.exchange)
	f.tokens[token] = issuedToken{at: f.clock, key: f.key}
	body := map[string]any{
		"token":      token,
		"expires_in": int(testTokenLifetime.Seconds()),
		"issued_at":  f.clock.Format(time.RFC3339),
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		f.t.Errorf("encode token: %v", err)
	}
}

func isBlobCommit(r *http.Request) bool {
	return r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/blobs/uploads/")
}

func TestPushRenewsTokenBeforeExpiry(t *testing.T) {
	fake := newFakeRegistry(t, false)
	step := testTokenLifetime*3/5 + time.Second
	fake.afterReq = func(f *fakeRegistry, r *http.Request) {
		if isBlobCommit(r) {
			f.clock = f.clock.Add(step)
		}
	}
	client, delays := fake.client(t, remote.WithJobs(1))
	dir, _ := imageLayout(t)

	if _, err := client.PushLayout(dir, fake.host+"/"+testRepository+":build-1", false); err != nil {
		t.Fatalf("PushLayout: %v", err)
	}

	if elapsed := fake.now().Sub(time.Unix(0, 0)); elapsed < 2*testTokenLifetime {
		t.Fatalf("push spanned %s, want more than two token lifetimes", elapsed)
	}
	if want := 1 + imageBlobs; fake.exchange != want {
		t.Errorf("exchanges = %d, want %d", fake.exchange, want)
	}
	if fake.rejected != 0 {
		t.Errorf("registry rejected %d expired tokens, want none", fake.rejected)
	}
	if len(*delays) != 0 {
		t.Errorf("backed off %v, want no retries", *delays)
	}
}

func TestPushRecoversFromKeySwap(t *testing.T) {
	fake := newFakeRegistry(t, false)
	fake.afterReq = func(f *fakeRegistry, r *http.Request) {
		if isBlobCommit(r) && f.key == 0 {
			f.key++
		}
	}
	client, delays := fake.client(t, remote.WithJobs(1))
	dir, _ := imageLayout(t)

	if _, err := client.PushLayout(dir, fake.host+"/"+testRepository+":build-1", false); err != nil {
		t.Fatalf("PushLayout: %v", err)
	}

	if fake.rejected != 1 {
		t.Errorf("registry rejected %d tokens, want 1", fake.rejected)
	}
	if fake.exchange != 2 {
		t.Errorf("exchanges = %d, want 2", fake.exchange)
	}
	if want := []time.Duration{firstRetryDelay}; !slices.Equal(*delays, want) {
		t.Errorf("backed off %v, want %v", *delays, want)
	}
}

func TestRestoreRequestsPullScope(t *testing.T) {
	fake := newFakeRegistry(t, false)
	client, _ := fake.client(t)
	dir, _ := imageLayout(t)
	digest, err := client.PushLayout(dir, fake.host+"/"+testRepository+":buildcache-1", true)
	if err != nil {
		t.Fatalf("PushLayout: %v", err)
	}

	fake.mu.Lock()
	fake.scope = "repository:" + testRepository + ":" + transport.PullScope
	fake.mu.Unlock()
	ref := fake.host + "/" + testRepository + "@" + digest.String()
	if err := client.RestoreCache(ref, t.TempDir(), 0); err != nil {
		t.Fatalf("RestoreCache: %v", err)
	}
}

func TestRefusingRealmFailsAtOnce(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			fake := newFakeRegistry(t, false)
			fake.refuse = status
			client, delays := fake.client(t)
			dir, _ := imageLayout(t)

			_, err := client.PushLayout(dir, fake.host+"/"+testRepository+":build-1", false)
			if !errors.Is(err, errCredentialRejected) {
				t.Fatalf("PushLayout = %v, want errCredentialRejected", err)
			}
			if !strings.Contains(err.Error(), fake.host) {
				t.Errorf("error %q does not name the registry %s", err, fake.host)
			}
			if fake.exchange != 1 {
				t.Errorf("exchanges = %d, want 1", fake.exchange)
			}
			if len(*delays) != 0 {
				t.Errorf("backed off %v, want no retries", *delays)
			}
		})
	}
}

func TestUnavailableRealmStopsAfterBound(t *testing.T) {
	fake := newFakeRegistry(t, false)
	fake.refuse = http.StatusServiceUnavailable
	client, delays := fake.client(t)
	dir, _ := imageLayout(t)

	_, err := client.PushLayout(dir, fake.host+"/"+testRepository+":build-1", false)
	if !errors.Is(err, errExchangeFailed) {
		t.Fatalf("PushLayout = %v, want errExchangeFailed", err)
	}
	if !strings.Contains(err.Error(), fake.host) {
		t.Errorf("error %q does not name the registry %s", err, fake.host)
	}
	if fake.exchange != maxTokenAttempts {
		t.Errorf("exchanges = %d, want %d", fake.exchange, maxTokenAttempts)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	if !slices.Equal(*delays, want) {
		t.Errorf("backed off %v, want %v", *delays, want)
	}
}

func TestRejectedTokensStopAfterBound(t *testing.T) {
	fake := newFakeRegistry(t, false)
	fake.revoked = true
	client, delays := fake.client(t)
	dir, _ := imageLayout(t)

	_, err := client.PushLayout(dir, fake.host+"/"+testRepository+":build-1", false)
	if !errors.Is(err, errTokenRejected) {
		t.Fatalf("PushLayout = %v, want errTokenRejected", err)
	}
	if !strings.Contains(err.Error(), fake.host) {
		t.Errorf("error %q does not name the registry %s", err, fake.host)
	}
	if fake.rejected != maxTokenAttempts {
		t.Errorf("registry rejected %d tokens, want %d", fake.rejected, maxTokenAttempts)
	}
	if len(*delays) != maxTokenAttempts-1 {
		t.Errorf("backed off %v, want %d delays", *delays, maxTokenAttempts-1)
	}
}

func TestBasicChallengeUsesKeychain(t *testing.T) {
	fake := newFakeRegistry(t, true)
	client, _ := fake.client(t)
	dir, _ := imageLayout(t)

	if _, err := client.PushLayout(dir, fake.host+"/"+testRepository+":build-1", false); err != nil {
		t.Fatalf("PushLayout: %v", err)
	}
	if fake.exchange != 0 {
		t.Errorf("exchanges = %d, want none for a Basic challenge", fake.exchange)
	}
}

func TestParallelPushRenewsTokens(t *testing.T) {
	fake := newFakeRegistry(t, false)
	step := testTokenLifetime*3/5 + time.Second
	fake.afterReq = func(f *fakeRegistry, r *http.Request) {
		if isBlobCommit(r) {
			f.clock = f.clock.Add(step)
		}
	}
	client, _ := fake.client(t)
	dir, _ := imageLayout(t)

	if _, err := client.PushLayout(dir, fake.host+"/"+testRepository+":build-1", false); err != nil {
		t.Fatalf("PushLayout: %v", err)
	}
	if elapsed := fake.now().Sub(time.Unix(0, 0)); elapsed < 2*testTokenLifetime {
		t.Fatalf("push spanned %s, want more than two token lifetimes", elapsed)
	}
	if fake.exchange < 2 {
		t.Errorf("exchanges = %d, want at least 2", fake.exchange)
	}
}

func TestPermanentExchangeErrorFailsAtOnce(t *testing.T) {
	tests := []struct {
		name          string
		realm         string
		body          any
		wantExchanges int
	}{
		{name: "private realm address", realm: "http://10.0.0.1/token", wantExchanges: 0},
		{name: "no token in response", body: map[string]any{}, wantExchanges: 1},
		{name: "response is not a token object", body: "not a token", wantExchanges: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeRegistry(t, false)
			if tt.realm != "" {
				fake.realm = tt.realm
			}
			fake.body = tt.body
			client, delays := fake.client(t)
			dir, _ := imageLayout(t)

			_, err := client.PushLayout(dir, fake.host+"/"+testRepository+":build-1", false)
			if !errors.Is(err, errExchangeFailed) {
				t.Fatalf("PushLayout = %v, want errExchangeFailed", err)
			}
			if !strings.Contains(err.Error(), fake.host) {
				t.Errorf("error %q does not name the registry %s", err, fake.host)
			}
			if fake.exchange != tt.wantExchanges {
				t.Errorf("exchanges = %d, want %d", fake.exchange, tt.wantExchanges)
			}
			if len(*delays) != 0 {
				t.Errorf("backed off %v, want no retries", *delays)
			}
		})
	}
}

func TestPingRetriesTransientFailure(t *testing.T) {
	fake := newFakeRegistry(t, false)
	fake.pingFail = 1
	client, _ := fake.client(t)
	dir, _ := imageLayout(t)

	if _, err := client.PushLayout(dir, fake.host+"/"+testRepository+":build-1", false); err != nil {
		t.Fatalf("PushLayout: %v", err)
	}
	if fake.pings < 2 {
		t.Errorf("pings = %d, want the failed ping repeated", fake.pings)
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

func TestRoundTripClosesBodyOnError(t *testing.T) {
	fake := newFakeRegistry(t, false)
	fake.refuse = http.StatusUnauthorized
	reg, err := name.NewRegistry(fake.host, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := transport.Ping(t.Context(), reg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	tokens := &tokenTransport{
		inner:     http.DefaultTransport,
		exchange:  http.DefaultTransport,
		registry:  reg,
		challenge: challenge,
		auth:      authn.Anonymous,
		scopes:    []string{fake.scope},
		now:       fake.now,
		sleep:     func(context.Context, time.Duration) error { return nil },
	}
	body := &trackedBody{Reader: strings.NewReader("manifest")}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "http://"+fake.host+"/v2/", body)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := tokens.RoundTrip(req)
	if err == nil {
		closeErr := resp.Body.Close()
		t.Fatalf("RoundTrip succeeded with a refused credential (close: %v)", closeErr)
	}
	if !body.closed {
		t.Error("RoundTrip returned an error without closing the request body")
	}
}

func TestBlobRedirectDropsAuthorization(t *testing.T) {
	fake := newFakeRegistry(t, false)
	var mu sync.Mutex
	var headers []string
	bucket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Get("Authorization"))
		mu.Unlock()
		fake.inner.ServeHTTP(w, r)
	}))
	t.Cleanup(bucket.Close)
	client, _ := fake.client(t)
	dir, _ := imageLayout(t)
	digest, err := client.PushLayout(dir, fake.host+"/"+testRepository+":buildcache-1", true)
	if err != nil {
		t.Fatalf("PushLayout: %v", err)
	}

	fake.mu.Lock()
	fake.scope = "repository:" + testRepository + ":" + transport.PullScope
	fake.redirect = bucket.URL
	fake.mu.Unlock()
	ref := fake.host + "/" + testRepository + "@" + digest.String()
	if err := client.RestoreCache(ref, t.TempDir(), 0); err != nil {
		t.Fatalf("RestoreCache: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(headers) == 0 {
		t.Fatal("restore never followed a redirect to the bucket")
	}
	for _, header := range headers {
		if header != "" {
			t.Errorf("bucket request carried Authorization %q", header)
		}
	}
}
