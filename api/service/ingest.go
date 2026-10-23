package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/rpcerr"
)

const jwksContentType = "application/json"

var (
	errIngestNotConfigured = errors.New("telemetry ingest tokens are not configured")
	errClusterRevoked      = errors.New("cluster is removed")
)

type IngestConfig struct {
	SigningKeys   []ingesttoken.SigningKey
	TokenLifetime time.Duration
}

type IngestServer struct {
	queries  genDb.Querier
	clusters *ClusterAuthenticator
	config   IngestConfig
	keySet   jose.JSONWebKeySet
}

func NewIngestServer(queries genDb.Querier, clusters *ClusterAuthenticator, config IngestConfig) *IngestServer {
	return &IngestServer{
		queries:  queries,
		clusters: clusters,
		config:   config,
		keySet:   ingesttoken.PublicKeySet(config.SigningKeys),
	}
}

func (s *IngestServer) IssueToken(
	ctx context.Context,
	_ *ingestv1.IssueTokenRequest,
) (*ingestv1.IssueTokenResponse, error) {
	if len(s.config.SigningKeys) == 0 {
		return nil, rpcerr.New(connect.CodeFailedPrecondition, errIngestNotConfigured)
	}
	info, _ := connect.CallInfoForServerContext(ctx)
	cluster, err := s.clusters.Authenticate(ctx, info.RequestHeader().Get("Authorization"))
	if err != nil {
		return nil, agentAuthError(err)
	}
	clusterID := cluster.ID.String()
	token, expiry, err := ingesttoken.Sign(s.config.SigningKeys[0], clusterID, time.Now(), s.config.TokenLifetime)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, publicRequestFailed).WithCause(err)
	}
	expiresAt := timestamppb.New(expiry)
	return &ingestv1.IssueTokenResponse{Token: token, ExpiresAt: expiresAt}, nil
}

func (s *IngestServer) ListPlacementWindows(
	ctx context.Context,
	_ *ingestv1.ListPlacementWindowsRequest,
) (*ingestv1.ListPlacementWindowsResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	token := extractBearerToken(info.RequestHeader().Get("Authorization"))
	claims, err := ingesttoken.Verify(token, s.keySet, time.Now())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	clusterID, err := uuid.Parse(claims.ClusterID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	_, err = s.queries.GetEnrolledCluster(ctx, clusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodePermissionDenied, errClusterRevoked)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
	}
	rows, err := s.queries.ListClusterPlacementWindows(ctx, clusterID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
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
	return &ingestv1.ListPlacementWindowsResponse{Windows: windows}, nil
}

func (s *IngestServer) JWKS(w http.ResponseWriter, r *http.Request) {
	body, err := json.Marshal(s.keySet)
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
