package ingesttest

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/gen/go/loco/ingest/v1/ingestv1connect"
	"github.com/team-loco/loco/internal/ingesttoken"
)

const (
	bearerPrefix = "Bearer "
	revoked      = "cluster is removed"
	outage       = "database unavailable"
	unauthorized = "unauthorized"
)

type State struct {
	Keys    []ingesttoken.SigningKey
	Windows []*ingestv1.PlacementWindow
	Revoked bool
	Outage  bool
}

type ControlPlane struct {
	ingestv1connect.UnimplementedIngestServiceHandler

	mu        sync.Mutex
	state     State
	listCalls int
	jwksCalls int
	server    *httptest.Server
}

func Start(keys []ingesttoken.SigningKey) *ControlPlane {
	return Serve(nil, keys)
}

func Serve(listener net.Listener, keys []ingesttoken.SigningKey) *ControlPlane {
	cp := &ControlPlane{state: State{Keys: keys}}
	rpcServer := connect.NewServer()
	ingestv1connect.RegisterIngestServiceHandler(rpcServer, cp)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	mux.HandleFunc("GET "+ingesttoken.JWKSPath, cp.serveJWKS)
	cp.server = httptest.NewUnstartedServer(mux)
	if listener != nil {
		cp.server.Listener = listener
	}
	cp.server.Start()
	return cp
}

func (c *ControlPlane) URL() string {
	return c.server.URL
}

func (c *ControlPlane) Close() {
	c.server.Close()
}

func (c *ControlPlane) Update(update func(*State)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	update(&c.state)
}

func (c *ControlPlane) SigningKey() ingesttoken.SigningKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.Keys[0]
}

func (c *ControlPlane) Place(workspaceID string, placedAt, removedAt time.Time) {
	window := &ingestv1.PlacementWindow{WorkspaceId: workspaceID, PlacedAt: timestamppb.New(placedAt)}
	if !removedAt.IsZero() {
		window.RemovedAt = timestamppb.New(removedAt)
	}
	c.Update(func(s *State) { s.Windows = append(s.Windows, window) })
}

func (c *ControlPlane) Calls() (list, jwks int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listCalls, c.jwksCalls
}

func (c *ControlPlane) serveJWKS(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	c.jwksCalls++
	set := ingesttoken.PublicKeySet(c.state.Keys)
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

func (c *ControlPlane) ListPlacementWindows(
	ctx context.Context,
	_ *ingestv1.ListPlacementWindowsRequest,
) (*ingestv1.ListPlacementWindowsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listCalls++
	info, _ := connect.CallInfoForServerContext(ctx)
	token, _ := strings.CutPrefix(info.RequestHeader().Get("Authorization"), bearerPrefix)
	keys := ingesttoken.PublicKeySet(c.state.Keys)
	if _, err := ingesttoken.Verify(token, keys, time.Now()); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, unauthorized)
	}
	if c.state.Outage {
		return nil, connect.NewError(connect.CodeUnavailable, outage)
	}
	if c.state.Revoked {
		return nil, connect.NewError(connect.CodePermissionDenied, revoked)
	}
	return &ingestv1.ListPlacementWindowsResponse{Windows: c.state.Windows}, nil
}
