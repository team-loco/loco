package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/client"
	"github.com/team-loco/loco/api/gen/db"
	registryv1 "github.com/team-loco/loco/shared/proto/registry/v1"
)

// RegistryServer implements the RegistryService
type RegistryServer struct {
	db                *pgxpool.Pool
	queries           db.Querier
	gitlabURL         string
	gitlabPAT         string
	gitlabProjectID   string
	deployTokenName   string
	registryBaseImage string
	httpClient        *http.Client
}

// NewRegistryServer creates a new RegistryServer instance
func NewRegistryServer(
	dbPool *pgxpool.Pool,
	queries db.Querier,
	gitlabURL string,
	gitlabPAT string,
	gitlabProjectID string,
	deployTokenName string,
	registryBaseImage string,
	httpClient *http.Client,
) *RegistryServer {
	return &RegistryServer{
		db:                dbPool,
		queries:           queries,
		gitlabURL:         gitlabURL,
		gitlabPAT:         gitlabPAT,
		gitlabProjectID:   gitlabProjectID,
		deployTokenName:   deployTokenName,
		registryBaseImage: registryBaseImage,
		httpClient:        httpClient,
	}
}

// GitlabToken generates a short-lived deploy token for Docker registry authentication
func (s *RegistryServer) GitlabToken(
	ctx context.Context,
	req *connect.Request[registryv1.GitlabTokenRequest],
) (*connect.Response[registryv1.GitlabTokenResponse], error) {
	expiresAt := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
	payload := map[string]any{
		"name":       s.deployTokenName,
		"scopes":     []string{"write_registry", "read_registry"},
		"expires_at": expiresAt,
	}

	gitlabClient := client.NewGitlabClient(s.gitlabURL, s.httpClient)
	tokenResp, err := gitlabClient.CreateDeployToken(ctx, s.gitlabPAT, s.gitlabProjectID, payload)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create gitlab deploy token", slog.String("error", err.Error()))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create deploy token: %w", err))
	}

	res := connect.NewResponse(&registryv1.GitlabTokenResponse{
		Username: tokenResp.Username,
		Token:    tokenResp.Token,
	})

	slog.DebugContext(ctx, "generated gitlab deploy token successfully", slog.String("username", tokenResp.Username))
	return res, nil
}
