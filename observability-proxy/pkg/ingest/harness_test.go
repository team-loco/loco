package ingest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/gen/go/loco/ingest/v1/ingestv1connect"
	"github.com/team-loco/loco/internal/ingesttoken"
)

const (
	testClusterID         = "0199e0a4-0000-7000-8000-0000000000c1"
	testOtherWorkspaceID  = "22222222-3333-4444-5555-666666666666"
	testTokenLifetime     = time.Hour
	testPlacedFor         = time.Hour
	testClockSkew         = time.Minute
	testAuthTimeout       = 5 * time.Second
	testJWKSRefresh       = time.Hour
	testPlacementTTL      = time.Hour
	testPlacementRefetch  = time.Nanosecond
	testRetryAfter        = 30 * time.Second
	testCurrentKeyID      = "current"
	testRotatedKeyID      = "rotated"
	controlPlaneRevoked   = "cluster is removed"
	controlPlaneOutage    = "database unavailable"
	controlPlaneBadBearer = "unauthorized"
)

type controlPlane struct {
	ingestv1connect.UnimplementedIngestServiceHandler

	t         *testing.T
	mu        sync.Mutex
	keys      []ingesttoken.SigningKey
	windows   []*ingestv1.PlacementWindow
	revoked   bool
	outage    bool
	listCalls int
	jwksCalls int
	url       string
}

func newSigningKey(t *testing.T, id string) ingesttoken.SigningKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return ingesttoken.SigningKey{ID: id, Key: key}
}

func newControlPlane(t *testing.T) *controlPlane {
	t.Helper()
	cp := &controlPlane{t: t, keys: []ingesttoken.SigningKey{newSigningKey(t, testCurrentKeyID)}}
	rpcServer := connect.NewServer()
	ingestv1connect.RegisterIngestServiceHandler(rpcServer, cp)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	mux.HandleFunc("GET "+ingesttoken.JWKSPath, cp.serveJWKS)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	cp.url = server.URL
	return cp
}

func (c *controlPlane) serveJWKS(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	c.jwksCalls++
	set := ingesttoken.PublicKeySet(c.keys)
	c.mu.Unlock()
	body, err := json.Marshal(set)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if _, err := w.Write(body); err != nil {
		return
	}
}

func (c *controlPlane) ListPlacementWindows(
	ctx context.Context,
	_ *ingestv1.ListPlacementWindowsRequest,
) (*ingestv1.ListPlacementWindowsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listCalls++
	info, _ := connect.CallInfoForServerContext(ctx)
	token := info.RequestHeader().Get(authorizationHeader)[len(bearerPrefix):]
	if _, err := ingesttoken.Verify(token, ingesttoken.PublicKeySet(c.keys), time.Now()); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, controlPlaneBadBearer)
	}
	if c.outage {
		return nil, connect.NewError(connect.CodeUnavailable, controlPlaneOutage)
	}
	if c.revoked {
		return nil, connect.NewError(connect.CodePermissionDenied, controlPlaneRevoked)
	}
	return &ingestv1.ListPlacementWindowsResponse{Windows: c.windows}, nil
}

func (c *controlPlane) place(workspaceID string, placedAt, removedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	window := &ingestv1.PlacementWindow{WorkspaceId: workspaceID, PlacedAt: timestamppb.New(placedAt)}
	if !removedAt.IsZero() {
		window.RemovedAt = timestamppb.New(removedAt)
	}
	c.windows = append(c.windows, window)
}

func (c *controlPlane) set(update func(*controlPlane)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	update(c)
}

func (c *controlPlane) calls() (list, jwks int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listCalls, c.jwksCalls
}

func (c *controlPlane) token(issuedAt time.Time) string {
	c.t.Helper()
	c.mu.Lock()
	key := c.keys[0]
	c.mu.Unlock()
	token, _, err := ingesttoken.Sign(key, testClusterID, issuedAt, testTokenLifetime)
	if err != nil {
		c.t.Fatal(err)
	}
	return token
}

func (c *controlPlane) bearer() string {
	c.t.Helper()
	return bearerPrefix + c.token(time.Now())
}

func (c *controlPlane) gate() *Gate {
	return NewGate(GateConfig{
		ControlPlaneURL:     c.url,
		RequestTimeout:      testAuthTimeout,
		JWKSRefreshInterval: testJWKSRefresh,
		PlacementTTL:        testPlacementTTL,
		RefetchInterval:     testPlacementRefetch,
		ClockSkew:           testClockSkew,
	})
}

func newGatedService(consumers Consumers, cp *controlPlane) *Service {
	cfg := ServiceConfig{MaxRequestBytes: testMaxRequestBytes, RetryAfter: testRetryAfter}
	return NewService(consumers, cp.gate(), cfg, prometheus.NewRegistry())
}

func newPlacedControlPlane(t *testing.T) *controlPlane {
	t.Helper()
	cp := newControlPlane(t)
	cp.place(testWorkspaceID, time.Now().Add(-testPlacedFor), time.Time{})
	return cp
}
