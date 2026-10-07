package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/clusternotify"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

var (
	ErrAgentNotAuthenticated = errors.New("agent not authenticated")
	ErrInvalidAgentToken     = errors.New("invalid agent token")
)

// AgentServer implements the AgentService for agent communication.
type AgentServer struct {
	db       *pgxpool.Pool
	queries  genDb.Querier
	notifier *clusternotify.Notifier
}

// NewAgentServer creates a new AgentServer instance.
func NewAgentServer(db *pgxpool.Pool, queries genDb.Querier, notifier *clusternotify.Notifier) *AgentServer {
	return &AgentServer{
		db:       db,
		queries:  queries,
		notifier: notifier,
	}
}

// hashToken creates a SHA256 hash of the token for secure storage/lookup.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// extractBearerToken extracts the token from Authorization header.
func extractBearerToken(header string) string {
	if after, ok := strings.CutPrefix(header, "Bearer "); ok {
		return after
	}
	return ""
}

// authenticateAgent validates the agent token and returns the cluster.
func (s *AgentServer) authenticateAgent(
	ctx context.Context,
	authHeader string,
) (genDb.GetClusterByAgentTokenRow, error) {
	token := extractBearerToken(authHeader)
	if token == "" {
		return genDb.GetClusterByAgentTokenRow{}, ErrInvalidAgentToken
	}

	tokenHash := hashToken(token)
	cluster, err := s.queries.GetClusterByAgentToken(ctx, &tokenHash)
	if err != nil {
		slog.WarnContext(ctx, "invalid agent token", "error", err)
		return genDb.GetClusterByAgentTokenRow{}, ErrInvalidAgentToken
	}

	return cluster, nil
}

// Register announces an agent to the control plane.
func (s *AgentServer) Register(
	ctx context.Context,
	req *connect.Request[agentv1.RegisterRequest],
) (*connect.Response[agentv1.RegisterResponse], error) {
	r := req.Msg

	// Authenticate using bearer token
	cluster, err := s.authenticateAgent(ctx, req.Header().Get("Authorization"))
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	// Update cluster with agent info
	agentVersion := r.GetAgentVersion()
	cpuCores := r.GetCapacity().GetCpuMillicoresTotal()
	memBytes := r.GetCapacity().GetMemoryBytesTotal()
	err = s.queries.UpdateClusterAgentInfo(ctx, genDb.UpdateClusterAgentInfoParams{
		ID:                    cluster.ID,
		AgentVersion:          &agentVersion,
		CapacityCpuMillicores: &cpuCores,
		CapacityMemoryBytes:   &memBytes,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update cluster agent info", "error", err, "cluster_id", cluster.ID)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	slog.InfoContext(ctx, "agent registered",
		"cluster_id", cluster.ID,
		"cluster_name", cluster.Name,
		"region", r.GetRegion(),
		"agent_version", r.GetAgentVersion(),
	)

	return connect.NewResponse(&agentv1.RegisterResponse{
		ClusterId: cluster.ID.String(),
	}), nil
}

// Heartbeat handles bidirectional heartbeat streaming.
func (s *AgentServer) Heartbeat(
	ctx context.Context,
	stream *connect.BidiStream[agentv1.HeartbeatRequest, agentv1.HeartbeatResponse],
) error {
	// Authenticate on first message
	cluster, err := s.authenticateAgent(ctx, stream.RequestHeader().Get("Authorization"))
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}

	slog.InfoContext(ctx, "heartbeat stream opened", "cluster_id", cluster.ID)

	for {
		req, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		// Update cluster heartbeat in database
		cpuCores := req.GetCapacity().GetCpuMillicoresTotal()
		memBytes := req.GetCapacity().GetMemoryBytesTotal()
		healthStatus := healthStatusFromProto(req.GetHealth())
		err = s.queries.UpdateClusterHeartbeat(ctx, genDb.UpdateClusterHeartbeatParams{
			ID:                    cluster.ID,
			LastHeartbeat:         func() *time.Time { t := time.Now(); return &t }(),
			CapacityCpuMillicores: &cpuCores,
			CapacityMemoryBytes:   &memBytes,
			HealthStatus:          &healthStatus,
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to update heartbeat", "error", err, "cluster_id", cluster.ID)
		}
	}
}

// healthStatusFromProto converts agent health to a status string.
const (
	clusterHealthHealthy   = "healthy"
	clusterHealthDegraded  = "degraded"
	clusterHealthUnhealthy = "unhealthy"
)

func healthStatusFromProto(h *agentv1.AgentHealth) string {
	if h == nil {
		return clusterHealthDegraded
	}
	if h.GetKubernetesHealthy() && h.GetControllerHealthy() {
		return clusterHealthHealthy
	}
	if !h.GetKubernetesHealthy() && !h.GetControllerHealthy() {
		return clusterHealthUnhealthy
	}
	return clusterHealthDegraded
}
