package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/go-jose/go-jose/v4"

	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/gen/go/loco/ingest/v1/ingestv1connect"
	"github.com/team-loco/loco/internal/ingesttoken"
)

const bearerPrefix = "Bearer "

var (
	ErrUnauthenticated = errors.New("missing or invalid ingest token")
	ErrTokenExpired    = errors.New("ingest token expired")
	ErrClusterRevoked  = errors.New("cluster may not send telemetry")
	ErrAuthUnavailable = errors.New("ingest authorization unavailable")

	errJWKSStatus = errors.New("unexpected JWKS response status")
	errJWKSRecent = errors.New("token signed by an unknown key and the JWKS was refreshed recently")
)

type GateConfig struct {
	ControlPlaneURL     string
	RequestTimeout      time.Duration
	JWKSRefreshInterval time.Duration
	PlacementTTL        time.Duration
	RefetchInterval     time.Duration
	ClockSkew           time.Duration
}

type Gate struct {
	cfg        GateConfig
	http       *http.Client
	placements ingestv1connect.IngestServiceClient

	keysMu      sync.Mutex
	keys        jose.JSONWebKeySet
	keysFetched time.Time

	clustersMu sync.Mutex
	clusters   map[string]*clusterPlacements
}

type window struct {
	workspaceID string
	placedAt    time.Time
	removedAt   time.Time
}

type clusterPlacements struct {
	mu        sync.Mutex
	windows   []window
	revoked   bool
	fetchedAt time.Time
}

func NewGate(cfg GateConfig) *Gate {
	httpClient := &http.Client{Timeout: cfg.RequestTimeout}
	transport := connecthttp.NewTransport(httpClient, cfg.ControlPlaneURL)
	client := ingestv1connect.NewIngestServiceClient(connect.NewClient(transport))
	return &Gate{cfg: cfg, http: httpClient, placements: client, clusters: map[string]*clusterPlacements{}}
}

func (g *Gate) Admit(ctx context.Context, authorization string) (*Admission, error) {
	token, ok := strings.CutPrefix(authorization, bearerPrefix)
	if !ok || token == "" {
		return nil, ErrUnauthenticated
	}
	claims, err := g.verify(ctx, token)
	if err != nil {
		return nil, err
	}
	cluster := g.cluster(claims.ClusterID)
	windows, err := cluster.load(ctx, g, token, g.cfg.PlacementTTL)
	if err != nil {
		return nil, err
	}
	return &Admission{gate: g, cluster: cluster, token: token, ClusterID: claims.ClusterID, windows: windows}, nil
}

func (g *Gate) verify(ctx context.Context, token string) (ingesttoken.Claims, error) {
	keys, _, err := g.keySet(ctx, false)
	if err != nil {
		return ingesttoken.Claims{}, err
	}
	claims, err := ingesttoken.Verify(token, keys, time.Now())
	if errors.Is(err, ingesttoken.ErrUnknownKey) {
		var refreshed bool
		if keys, refreshed, err = g.keySet(ctx, true); err != nil {
			return ingesttoken.Claims{}, err
		}
		if !refreshed {
			return ingesttoken.Claims{}, fmt.Errorf("%w: %w", ErrAuthUnavailable, errJWKSRecent)
		}
		claims, err = ingesttoken.Verify(token, keys, time.Now())
	}
	if errors.Is(err, ingesttoken.ErrExpired) {
		return ingesttoken.Claims{}, ErrTokenExpired
	}
	if err != nil {
		return ingesttoken.Claims{}, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	return claims, nil
}

func (g *Gate) keySet(ctx context.Context, unknownKey bool) (jose.JSONWebKeySet, bool, error) {
	g.keysMu.Lock()
	defer g.keysMu.Unlock()
	fetched := !g.keysFetched.IsZero()
	if fetched && (!unknownKey || time.Since(g.keysFetched) < g.cfg.JWKSRefreshInterval) {
		return g.keys, false, nil
	}
	keys, err := g.fetchKeys(ctx)
	if err != nil {
		return jose.JSONWebKeySet{}, false, fmt.Errorf("%w: %w", ErrAuthUnavailable, err)
	}
	g.keys = keys
	g.keysFetched = time.Now()
	return keys, true, nil
}

func (g *Gate) fetchKeys(ctx context.Context) (jose.JSONWebKeySet, error) {
	url := strings.TrimSuffix(g.cfg.ControlPlaneURL, "/") + ingesttoken.JWKSPath
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return jose.JSONWebKeySet{}, fmt.Errorf("build JWKS request: %w", err)
	}
	response, err := g.http.Do(request)
	if err != nil {
		return jose.JSONWebKeySet{}, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return jose.JSONWebKeySet{}, fmt.Errorf("%w: %d", errJWKSStatus, response.StatusCode)
	}
	var keys jose.JSONWebKeySet
	if err := json.NewDecoder(response.Body).Decode(&keys); err != nil {
		return jose.JSONWebKeySet{}, fmt.Errorf("decode JWKS: %w", err)
	}
	return keys, nil
}

func (g *Gate) cluster(id string) *clusterPlacements {
	g.clustersMu.Lock()
	defer g.clustersMu.Unlock()
	cluster, ok := g.clusters[id]
	if !ok {
		cluster = &clusterPlacements{}
		g.clusters[id] = cluster
	}
	return cluster
}

func (c *clusterPlacements) load(ctx context.Context, g *Gate, token string, maxAge time.Duration) ([]window, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fetchedAt.IsZero() || time.Since(c.fetchedAt) >= maxAge {
		if err := c.fetch(ctx, g, token); err != nil {
			return nil, err
		}
	}
	if c.revoked {
		return nil, ErrClusterRevoked
	}
	return c.windows, nil
}

func (c *clusterPlacements) fetch(ctx context.Context, g *Gate, token string) error {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", bearerPrefix+token)
	resp, err := g.placements.ListPlacementWindows(ctx, &ingestv1.ListPlacementWindowsRequest{})
	if err != nil && connect.CodeOf(err) == connect.CodePermissionDenied {
		c.revoked = true
		c.windows = nil
		c.fetchedAt = time.Now()
		return nil
	}
	if err != nil && connect.CodeOf(err) == connect.CodeUnauthenticated {
		return fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	if err != nil {
		return fmt.Errorf("%w: list placement windows: %w", ErrAuthUnavailable, err)
	}
	windows := make([]window, 0, len(resp.GetWindows()))
	for _, w := range resp.GetWindows() {
		entry := window{workspaceID: w.GetWorkspaceId(), placedAt: w.GetPlacedAt().AsTime()}
		if w.GetRemovedAt() != nil {
			entry.removedAt = w.GetRemovedAt().AsTime()
		}
		windows = append(windows, entry)
	}
	c.windows = windows
	c.revoked = false
	c.fetchedAt = time.Now()
	return nil
}

type Admission struct {
	ClusterID string

	gate      *Gate
	cluster   *clusterPlacements
	token     string
	windows   []window
	refetched bool
}

type verdict string

const (
	accepted              verdict = ""
	rejectMissingTenancy  verdict = "missing_tenancy"
	rejectNotPlaced       verdict = "workspace_not_placed"
	rejectOutsideOfWindow verdict = "outside_placement_window"
)

func (a *Admission) check(ctx context.Context, workspaceID string, at time.Time) (verdict, error) {
	if workspaceID == "" {
		return rejectMissingTenancy, nil
	}
	result := a.match(workspaceID, at)
	if result == accepted || a.refetched {
		return result, nil
	}
	a.refetched = true
	windows, err := a.cluster.load(ctx, a.gate, a.token, a.gate.cfg.RefetchInterval)
	if err != nil {
		return accepted, err
	}
	a.windows = windows
	return a.match(workspaceID, at), nil
}

func (a *Admission) match(workspaceID string, at time.Time) verdict {
	skew := a.gate.cfg.ClockSkew
	result := rejectNotPlaced
	for _, w := range a.windows {
		if w.workspaceID != workspaceID {
			continue
		}
		result = rejectOutsideOfWindow
		if at.Before(w.placedAt.Add(-skew)) {
			continue
		}
		if !w.removedAt.IsZero() && !at.Before(w.removedAt.Add(skew)) {
			continue
		}
		return accepted
	}
	return result
}
