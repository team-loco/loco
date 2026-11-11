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
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/logretention"
	"github.com/team-loco/loco/internal/rpc"
)

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
	LogsTTL             time.Duration
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
	mu         sync.Mutex
	windows    []window
	retentions map[string]time.Duration
	revoked    bool
	fetchedAt  time.Time
}

func NewGate(cfg GateConfig) *Gate {
	httpClient := &http.Client{Timeout: cfg.RequestTimeout}
	transport := connecthttp.NewTransport(httpClient, cfg.ControlPlaneURL)
	client := ingestv1connect.NewIngestServiceClient(connect.NewClient(transport, rpc.RequestIDInterceptor))
	return &Gate{cfg: cfg, http: httpClient, placements: client, clusters: map[string]*clusterPlacements{}}
}

func (g *Gate) Admit(ctx context.Context, authorization string) (*Admission, error) {
	token, ok := bearer.Token(authorization)
	if !ok {
		return nil, ErrUnauthenticated
	}
	claims, err := g.verify(ctx, token)
	if err != nil {
		return nil, err
	}
	cluster := g.cluster(claims.ClusterID)
	snapshot, err := cluster.load(ctx, g, token, g.cfg.PlacementTTL)
	if err != nil {
		return nil, err
	}
	return &Admission{
		gate:       g,
		cluster:    cluster,
		token:      token,
		ClusterID:  claims.ClusterID,
		windows:    snapshot.windows,
		retentions: snapshot.retentions,
	}, nil
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

func (c *clusterPlacements) load(
	ctx context.Context,
	g *Gate,
	token string,
	maxAge time.Duration,
) (*placementSnapshot, error) {
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
	return &placementSnapshot{windows: c.windows, retentions: c.retentions}, nil
}

func (c *clusterPlacements) fetch(ctx context.Context, g *Gate, token string) error {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", bearer.Header(token))
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
	policies := make(map[string]time.Duration, len(resp.GetLogRetentions()))
	for _, policy := range resp.GetLogRetentions() {
		retention, err := logretention.Resolve(policy.GetRetentionPeriod(), time.Duration(1<<63-1))
		if err != nil {
			return fmt.Errorf("%w: resource log retention: %w", ErrAuthUnavailable, err)
		}
		policies[policy.GetWorkspaceId()+"/"+policy.GetResourceId()] = min(retention, g.cfg.LogsTTL)
	}
	c.retentions = policies
	c.windows = windows
	c.revoked = false
	c.fetchedAt = time.Now()
	return nil
}

type Admission struct {
	ClusterID string

	gate            *Gate
	cluster         *clusterPlacements
	token           string
	windows         []window
	retentions      map[string]time.Duration
	refetched       bool
	policyRefetched bool
}

type verdict string

const (
	accepted              verdict = ""
	rejectNotPlaced       verdict = "workspace_not_placed"
	rejectOutsideOfWindow verdict = "outside_placement_window"
)

func (a *Admission) check(ctx context.Context, workspaceID string, at time.Time) (verdict, error) {
	if workspaceID == "" {
		return accepted, nil
	}
	result := a.match(workspaceID, at)
	if result == accepted || a.refetched {
		return result, nil
	}
	if err := a.refresh(ctx, a.gate.cfg.RefetchInterval); err != nil {
		return accepted, err
	}

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

type placementSnapshot struct {
	windows    []window
	retentions map[string]time.Duration
}

func (a *Admission) refresh(ctx context.Context, maxAge time.Duration) error {
	a.refetched = true
	snapshot, err := a.cluster.load(ctx, a.gate, a.token, maxAge)
	if err != nil {
		return err
	}
	a.windows = snapshot.windows
	a.retentions = snapshot.retentions
	return nil
}

func (a *Admission) logRetention(ctx context.Context, workspaceID, resourceID string) (time.Duration, bool, error) {
	if workspaceID == "" || resourceID == "" {
		return a.gate.cfg.LogsTTL, true, nil
	}
	key := workspaceID + "/" + resourceID
	retention, found := a.retentions[key]
	if found || a.policyRefetched {
		return retention, found, nil
	}
	a.policyRefetched = true
	if err := a.refresh(ctx, 0); err != nil {
		return 0, false, err
	}
	retention, found = a.retentions[key]
	return retention, found, nil
}
