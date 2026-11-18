package ingesttest

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/gen/go/loco/ingest/v1/ingestv1connect"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/ingesttoken"
)

const (
	revoked      = "cluster is removed"
	outage       = "database unavailable"
	unauthorized = "unauthorized"
)

type State struct {
	Keys    []Signer
	Windows []*ingestv1.PlacementWindow
	// Retention applies to records that name no workspace.
	Retention *ingestv1.Retention
	// WorkspaceRetention is each workspace's effective retention.
	WorkspaceRetention map[string]*ingestv1.Retention
	Revoked            bool
	Outage             bool
}

// retention is what the fake control plane gives the cluster and each workspace it places.
func retention() *ingestv1.Retention {
	return &ingestv1.Retention{LogDays: 30, TraceDays: 7, MetricDays: 90}
}

type ControlPlane struct {
	ingestv1connect.UnimplementedIngestServiceHandler

	mu             sync.Mutex
	state          State
	listCalls      int
	jwksCalls      int
	retentionCalls [][]string
	server         *httptest.Server
}

func Start(keys []Signer) *ControlPlane {
	return Serve(nil, keys)
}

func Serve(listener net.Listener, keys []Signer) *ControlPlane {
	cp := &ControlPlane{state: State{
		Keys:               keys,
		Retention:          retention(),
		WorkspaceRetention: map[string]*ingestv1.Retention{},
	}}
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

func (c *ControlPlane) SigningKey() Signer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.Keys[0]
}

func (c *ControlPlane) Place(workspaceID string, placedAt, removedAt time.Time) {
	window := &ingestv1.PlacementWindow{
		WorkspaceId: workspaceID,
		PlacedAt:    timestamppb.New(placedAt),
	}
	if !removedAt.IsZero() {
		window.RemovedAt = timestamppb.New(removedAt)
	}
	c.Update(func(s *State) {
		s.Windows = append(s.Windows, window)
		s.WorkspaceRetention[workspaceID] = retention()
	})
}

func (c *ControlPlane) Calls() (list, jwks int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.listCalls, c.jwksCalls
}

// RetentionCalls returns the workspace IDs of each GetRetention call so far.
func (c *ControlPlane) RetentionCalls() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.retentionCalls)
}

func (c *ControlPlane) serveJWKS(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	c.jwksCalls++
	set := KeySet(c.state.Keys)
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
	req *ingestv1.ListPlacementWindowsRequest,
) (*ingestv1.ListPlacementWindowsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listCalls++
	info, _ := connect.CallInfoForServerContext(ctx)
	token, _ := bearer.FromHeader(info.RequestHeader())
	keys := KeySet(c.state.Keys)
	if _, err := ingesttoken.Verify(token, keys, time.Now()); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, unauthorized)
	}
	if c.state.Outage {
		return nil, connect.NewError(connect.CodeUnavailable, outage)
	}
	if c.state.Revoked {
		return nil, connect.NewError(connect.CodePermissionDenied, revoked)
	}
	if err := req.GetSince().CheckValid(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	return &ingestv1.ListPlacementWindowsResponse{Windows: c.state.Windows, Retention: c.state.Retention}, nil
}

func (c *ControlPlane) GetRetention(
	ctx context.Context,
	req *ingestv1.GetRetentionRequest,
) (*ingestv1.GetRetentionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retentionCalls = append(c.retentionCalls, req.GetWorkspaceIds())
	info, _ := connect.CallInfoForServerContext(ctx)
	token, _ := bearer.FromHeader(info.RequestHeader())
	if _, err := ingesttoken.Verify(token, KeySet(c.state.Keys), time.Now()); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, unauthorized)
	}
	if c.state.Outage {
		return nil, connect.NewError(connect.CodeUnavailable, outage)
	}
	retention := map[string]*ingestv1.Retention{}
	for _, id := range req.GetWorkspaceIds() {
		if days, ok := c.state.WorkspaceRetention[id]; ok {
			retention[id] = days
		}
	}
	return &ingestv1.GetRetentionResponse{Retention: retention}, nil
}
