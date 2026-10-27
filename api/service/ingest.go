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
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/rpcerr"
)

const jwksContentType = "application/json"

var errClusterRevoked = errors.New("cluster is removed")

type IngestConfig struct {
	TransitKey    string
	TokenLifetime time.Duration
}

type IngestSigner interface {
	ingesttoken.Signer
	PublicKeys(ctx context.Context) (map[string]ed25519.PublicKey, error)
}

type IngestServer struct {
	queries  genDb.Querier
	clusters *ClusterAuthenticator
	signer   IngestSigner
	lifetime time.Duration
}

func NewIngestServer(
	queries genDb.Querier,
	clusters *ClusterAuthenticator,
	signer IngestSigner,
	lifetime time.Duration,
) *IngestServer {
	return &IngestServer{queries: queries, clusters: clusters, signer: signer, lifetime: lifetime}
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
	token, expiry, err := ingesttoken.Sign(ctx, s.signer, clusterID, time.Now(), s.lifetime)
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
	info, _ := connect.CallInfoForServerContext(ctx)
	token := extractBearerToken(info.RequestHeader().Get("Authorization"))
	keys, err := s.keySet(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
	}
	claims, err := ingesttoken.Verify(token, keys, time.Now())
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
