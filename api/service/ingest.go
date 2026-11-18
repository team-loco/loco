package service

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/rpcerr"
)

const jwksContentType = "application/json"

var errClusterRevoked = errors.New("cluster is removed")

type IngestSigner interface {
	ingesttoken.Signer
	PublicKeys(ctx context.Context) (map[string]ed25519.PublicKey, error)
}

// IngestConfig sets how long ingest tokens live and which records the observability proxy accepts.
type IngestConfig struct {
	TokenLifetime time.Duration
	// MaxRecordLateness is how far in the past a workspace's record may be.
	MaxRecordLateness time.Duration
	// ClockSkew widens each placement window at both ends.
	ClockSkew time.Duration
}

// WindowKeep is how long a closed placement window can still match a record.
func (c IngestConfig) WindowKeep() time.Duration {
	return c.MaxRecordLateness + c.ClockSkew
}

type IngestServer struct {
	queries  genDb.Querier
	clusters *ClusterAuthenticator
	signer   IngestSigner
	cfg      IngestConfig
	counts   *configv1.CountLimits
}

func NewIngestServer(
	queries genDb.Querier,
	clusters *ClusterAuthenticator,
	signer IngestSigner,
	cfg IngestConfig,
	counts *configv1.CountLimits,
) *IngestServer {
	return &IngestServer{queries: queries, clusters: clusters, signer: signer, cfg: cfg, counts: counts}
}

func (s *IngestServer) keySet(ctx context.Context) (jose.JSONWebKeySet, error) {
	keys, err := s.signer.PublicKeys(ctx)
	if err != nil {
		return jose.JSONWebKeySet{}, fmt.Errorf("ingest signing keys: %w", err)
	}
	set := jose.JSONWebKeySet{Keys: make([]jose.JSONWebKey, 0, len(keys))}
	for keyID, key := range keys {
		set.Keys = append(set.Keys, ingesttoken.PublicKey(keyID, key))
	}
	return set, nil
}

func (s *IngestServer) IssueToken(
	ctx context.Context,
	_ *ingestv1.IssueTokenRequest,
) (*ingestv1.IssueTokenResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	cluster, err := s.clusters.Authenticate(ctx, info.RequestHeader().Get("Authorization"))
	if err != nil {
		return nil, agentAuthError(err)
	}
	clusterID := cluster.ID.String()
	token, expiry, err := ingesttoken.Sign(ctx, s.signer, clusterID, time.Now(), s.cfg.TokenLifetime)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
	}
	expiresAt := timestamppb.New(expiry)
	return &ingestv1.IssueTokenResponse{Token: token, ExpiresAt: expiresAt}, nil
}

func (s *IngestServer) ListPlacementWindows(
	ctx context.Context,
	_ *ingestv1.ListPlacementWindowsRequest,
) (*ingestv1.ListPlacementWindowsResponse, error) {
	clusterID, err := s.authenticateCluster(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListClusterPlacementWindows(ctx, genDb.ListClusterPlacementWindowsParams{
		ClusterID: clusterID,
		Cutoff:    time.Now().Add(-s.cfg.WindowKeep()),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
	}
	install, err := retention(limits.Install(s.counts))
	if err != nil {
		return nil, err
	}
	windows := make([]*ingestv1.PlacementWindow, 0, len(rows))
	for _, row := range rows {
		window := &ingestv1.PlacementWindow{
			WorkspaceId: row.WorkspaceID.String(),
			PlacedAt:    timestamppb.New(row.PlacedAt),
		}
		if row.RemovedAt != nil {
			window.RemovedAt = timestamppb.New(*row.RemovedAt)
		}
		windows = append(windows, window)
	}
	return &ingestv1.ListPlacementWindowsResponse{
		Windows:           windows,
		Retention:         install,
		MaxRecordLateness: durationpb.New(s.cfg.MaxRecordLateness),
		ClockSkew:         durationpb.New(s.cfg.ClockSkew),
	}, nil
}

func (s *IngestServer) GetRetention(
	ctx context.Context,
	req *ingestv1.GetRetentionRequest,
) (*ingestv1.GetRetentionResponse, error) {
	if _, err := s.authenticateCluster(ctx); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(req.GetWorkspaceIds()))
	for _, raw := range req.GetWorkspaceIds() {
		id, err := parseID("workspace_ids", raw)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	rows, err := s.queries.ListWorkspaceRetention(ctx, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
	}
	orgs := orgLimits{queries: s.queries, defaults: s.counts}
	days := make(map[string]*ingestv1.Retention, len(rows))
	for _, row := range rows {
		counts, limitsErr := orgs.get(ctx, row.OrgID)
		if limitsErr != nil {
			return nil, connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(limitsErr)
		}
		own := ownRetention(row.LogRetentionDays, row.TraceRetentionDays, row.MetricRetentionDays)
		effective, effectiveErr := effectiveRetention(counts, own)
		if effectiveErr != nil {
			return nil, effectiveErr
		}
		days[row.ID.String()] = &ingestv1.Retention{
			LogDays:    effective.GetLogDays(),
			TraceDays:  effective.GetTraceDays(),
			MetricDays: effective.GetMetricDays(),
		}
	}
	return &ingestv1.GetRetentionResponse{Retention: days}, nil
}

// authenticateCluster returns the enrolled cluster named by the request's ingest token.
func (s *IngestServer) authenticateCluster(ctx context.Context) (uuid.UUID, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	token, _ := bearer.FromHeader(info.RequestHeader())
	keys, err := s.keySet(ctx)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
	}
	claims, err := ingesttoken.Verify(token, keys, time.Now())
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	clusterID, err := uuid.Parse(claims.ClusterID)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	_, err = s.queries.GetEnrolledCluster(ctx, clusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, rpcerr.New(connect.CodePermissionDenied, errClusterRevoked)
	}
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
	}
	return clusterID, nil
}

// retention returns how many days to keep each signal under counts.
func retention(counts limits.Effective) (*ingestv1.Retention, error) {
	logs, err := limits.LogRetentionDays.Get(counts)
	if err != nil {
		return nil, err
	}
	traces, err := limits.TraceRetentionDays.Get(counts)
	if err != nil {
		return nil, err
	}
	metrics, err := limits.MetricRetentionDays.Get(counts)
	if err != nil {
		return nil, err
	}
	return &ingestv1.Retention{LogDays: logs, TraceDays: traces, MetricDays: metrics}, nil
}

func (s *IngestServer) JWKS(w http.ResponseWriter, r *http.Request) {
	keys, err := s.keySet(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "read ingest JWKS", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	body, err := json.Marshal(keys)
	if err != nil {
		slog.ErrorContext(r.Context(), "encode ingest JWKS", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", jwksContentType)
	if _, err := w.Write(body); err != nil {
		slog.WarnContext(r.Context(), "write ingest JWKS", "error", err)
	}
}
