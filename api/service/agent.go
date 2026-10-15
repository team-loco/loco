package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/clusternotify"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
)

var (
	ErrAgentNotAuthenticated     = errors.New("agent not authenticated")
	ErrInvalidAgentToken         = errors.New("invalid agent token")
	errInvalidClusterMetadata    = errors.New("invalid cluster identity, name, region or tier")
	errClusterAttachmentRejected = errors.New("cluster is removed or its region or tier changed")
)

type AgentConfig struct {
	ConnectionSecrets         []string
	PlacementRetryDelay       time.Duration
	PlacementMaxApplyAttempts int32
}

// AgentServer implements the AgentService for agent communication.
type AgentServer struct {
	db                *pgxpool.Pool
	queries           genDb.Querier
	notifier          *clusternotify.Notifier
	sources           SourceBucket
	provider          secretkeys.Provider
	config            AgentConfig
	heartbeatInterval time.Duration
}

// NewAgentServer creates a new AgentServer instance; a nil provider leaves every placement that declares
// secrets pending.
func NewAgentServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	notifier *clusternotify.Notifier,
	sources SourceBucket,
	provider secretkeys.Provider,
	heartbeatInterval time.Duration,
	config AgentConfig,
) *AgentServer {
	return &AgentServer{
		db:                db,
		queries:           queries,
		notifier:          notifier,
		sources:           sources,
		provider:          provider,
		config:            config,
		heartbeatInterval: heartbeatInterval,
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

func (s *AgentServer) authenticateSecret(authHeader string) error {
	token := extractBearerToken(authHeader)
	if token == "" {
		return ErrInvalidAgentToken
	}
	candidate := sha256.Sum256([]byte(token))
	matched := 0
	for _, secret := range s.config.ConnectionSecrets {
		expected := sha256.Sum256([]byte(secret))
		matched |= subtle.ConstantTimeCompare(candidate[:], expected[:])
	}
	if matched != 1 {
		return ErrInvalidAgentToken
	}
	return nil
}

func (s *AgentServer) authenticateAgent(
	ctx context.Context,
	authHeader, clusterKey string,
) (genDb.GetClusterByKeyRow, error) {
	if err := s.authenticateSecret(authHeader); err != nil {
		return genDb.GetClusterByKeyRow{}, err
	}
	key, err := uuid.Parse(clusterKey)
	if err != nil {
		return genDb.GetClusterByKeyRow{}, ErrInvalidAgentToken
	}
	cluster, err := s.queries.GetClusterByKey(ctx, &key)
	if err != nil {
		return genDb.GetClusterByKeyRow{}, ErrInvalidAgentToken
	}
	return cluster, nil
}

// Register announces an agent to the control plane.
func (s *AgentServer) Register(
	ctx context.Context,
	req *agentv1.RegisterRequest,
) (*agentv1.RegisterResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)

	// Authenticate using bearer token
	err := s.authenticateSecret(info.RequestHeader().Get("Authorization"))
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}

	key, parseErr := uuid.Parse(req.GetClusterKey())
	if parseErr != nil || key == uuid.Nil || req.GetDisplayName() == "" || req.GetClusterRegion() == "" ||
		stringToProtoEnvType(req.GetTier()) == environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errInvalidClusterMetadata.Error())
	}
	var cluster genDb.AttachClusterRow
	// Update cluster with agent info
	agentVersion := req.GetAgentVersion()
	cpuCores := req.GetCapacity().GetCpuMillicoresTotal()
	memBytes := req.GetCapacity().GetMemoryBytesTotal()
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		var attachErr error
		cluster, attachErr = qtx.AttachCluster(
			ctx,
			genDb.AttachClusterParams{
				ClusterKey: &key,
				Name:       req.GetDisplayName(),
				Region:     req.GetClusterRegion(),
				Tier:       req.GetTier(),
			},
		)
		if errors.Is(attachErr, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeFailedPrecondition, errClusterAttachmentRejected.Error())
		}
		if attachErr != nil {
			return attachErr
		}

		if updateErr := qtx.UpdateClusterAgentInfo(ctx, genDb.UpdateClusterAgentInfoParams{
			ID:                    cluster.ID,
			AgentVersion:          &agentVersion,
			CapacityCpuMillicores: &cpuCores,
			CapacityMemoryBytes:   &memBytes,
			BuildsEnabled:         req.GetBuildsEnabled(),
		}); updateErr != nil {
			return updateErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.ClusterRegistered,
			ActorType:   "agent",
			ActorID:     new(cluster.ID),
			SubjectType: events.SubjectCluster,
			SubjectID:   new(cluster.ID),
			Data:        map[string]any{events.FieldName: cluster.Name},
		})
	})
	if err != nil {
		if connect.CodeOf(err) == connect.CodeFailedPrecondition {
			return nil, err
		}
		slog.ErrorContext(ctx, "failed to update cluster agent info", "error", err, "cluster_id", cluster.ID)
		return nil, connect.NewError(connect.CodeInternal, publicRequestFailed).WithCause(err)
	}

	slog.InfoContext(ctx, "agent registered",
		"cluster_id", cluster.ID,
		"cluster_name", cluster.Name,
		"region", cluster.Region,
		"agent_version", req.GetAgentVersion(),
		"builds_enabled", req.GetBuildsEnabled(),
	)

	heartbeatInterval := durationpb.New(s.heartbeatInterval)
	return &agentv1.RegisterResponse{
		ClusterId:         cluster.ID.String(),
		HeartbeatInterval: heartbeatInterval,
	}, nil
}

func (s *AgentServer) recordHeartbeat(ctx context.Context, clusterID uuid.UUID, heartbeat *agentv1.Heartbeat) {
	now := time.Now()
	cpuCores := heartbeat.GetCapacity().GetCpuMillicoresTotal()
	memBytes := heartbeat.GetCapacity().GetMemoryBytesTotal()
	healthStatus := healthStatusFromProto(heartbeat.GetHealth())
	err := s.queries.UpdateClusterHeartbeat(ctx, genDb.UpdateClusterHeartbeatParams{
		ID:                    clusterID,
		LastHeartbeat:         &now,
		CapacityCpuMillicores: &cpuCores,
		CapacityMemoryBytes:   &memBytes,
		HealthStatus:          &healthStatus,
		BuildsEnabled:         heartbeat.GetBuildsEnabled(),
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update heartbeat", "error", err, "cluster_id", clusterID)
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
