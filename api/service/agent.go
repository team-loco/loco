package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"buf.build/go/protovalidate"
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
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	errInvalidClusterKey      = errors.New("invalid cluster key")
	errEnrollmentRejected     = errors.New("join token is invalid, expired or used, or belongs to another cluster")
	errClusterKeyEnrolled     = errors.New("this Kubernetes cluster is already enrolled as another cluster")
	errClusterKeyMismatch     = errors.New("agent runs in a different Kubernetes cluster than the one enrolled")
	errInvalidRegisterRequest = errors.New("invalid registration request")
)

type AgentConfig struct {
	AssertionMaxLifetime      time.Duration
	JoinTokenTTL              time.Duration
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
	clusters          *ClusterAuthenticator
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
		clusters:          NewClusterAuthenticator(queries, config.AssertionMaxLifetime),
		heartbeatInterval: heartbeatInterval,
	}
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func extractBearerToken(header string) string {
	if after, ok := strings.CutPrefix(header, "Bearer "); ok {
		return after
	}
	return ""
}

func agentAuthError(err error) error {
	if errors.Is(err, ErrInvalidAgentToken) {
		return connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	return connect.NewError(connect.CodeUnavailable, publicRequestFailed).WithCause(err)
}

type registeredCluster struct {
	id     uuid.UUID
	name   string
	region string
}

// Register announces an agent to the control plane.
func (s *AgentServer) Register(
	ctx context.Context,
	req *agentv1.RegisterRequest,
) (*agentv1.RegisterResponse, error) {
	var enrolled *genDb.GetEnrolledClusterRow
	if req.GetEnrollment() == nil {
		info, _ := connect.CallInfoForServerContext(ctx)
		row, authErr := s.clusters.Authenticate(ctx, info.RequestHeader().Get("Authorization"))
		if authErr != nil {
			return nil, agentAuthError(authErr)
		}
		enrolled = &row
	}
	if validationErr := protovalidate.Validate(req); validationErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errInvalidRegisterRequest.Error()).
			WithCause(validationErr)
	}
	key, parseErr := uuid.Parse(req.GetClusterKey())
	if parseErr != nil || key == uuid.Nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, errInvalidClusterKey)
	}
	var cluster registeredCluster
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		var authErr error
		if enrolled != nil {
			cluster, authErr = registeredAs(*enrolled, key)
		} else {
			cluster, authErr = enroll(ctx, qtx, key, req.GetEnrollment())
		}
		if authErr != nil {
			return authErr
		}
		agentVersion := req.GetAgentVersion()
		cpuCores := req.GetCapacity().GetCpuMillicoresTotal()
		memBytes := req.GetCapacity().GetMemoryBytesTotal()
		if updateErr := qtx.UpdateClusterAgentInfo(ctx, genDb.UpdateClusterAgentInfoParams{
			ID:                    cluster.id,
			AgentVersion:          &agentVersion,
			CapacityCpuMillicores: &cpuCores,
			CapacityMemoryBytes:   &memBytes,
			BuildsEnabled:         req.GetBuildsEnabled(),
			Domains:               reportedDomains(req.GetDomains()),
		}); updateErr != nil {
			return updateErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.ClusterRegistered,
			ActorType:   "agent",
			ActorID:     new(cluster.id),
			SubjectType: events.SubjectCluster,
			SubjectID:   new(cluster.id),
			Data:        map[string]any{events.FieldName: cluster.name},
		})
	})
	if err != nil {
		if connectErr, ok := errors.AsType[*connect.Error](err); ok {
			return nil, connectErr
		}
		slog.ErrorContext(ctx, "failed to register agent", "error", err, "cluster_id", cluster.id)
		return nil, connect.NewError(connect.CodeInternal, publicRequestFailed).WithCause(err)
	}

	slog.InfoContext(ctx, "agent registered",
		"cluster_id", cluster.id,
		"cluster_name", cluster.name,
		"region", cluster.region,
		"enrolled", req.GetEnrollment() != nil,
		"agent_version", req.GetAgentVersion(),
		"builds_enabled", req.GetBuildsEnabled(),
		"domains", req.GetDomains(),
	)

	heartbeatInterval := durationpb.New(s.heartbeatInterval)
	return &agentv1.RegisterResponse{
		ClusterId:         cluster.id.String(),
		HeartbeatInterval: heartbeatInterval,
	}, nil
}

func enroll(
	ctx context.Context,
	qtx *genDb.Queries,
	key uuid.UUID,
	enrollment *agentv1.Enrollment,
) (registeredCluster, error) {
	tokenHash := hashToken(enrollment.GetJoinToken())
	row, err := qtx.EnrollCluster(ctx, genDb.EnrollClusterParams{
		PublicKey:     enrollment.GetPublicKey(),
		ClusterKey:    &key,
		JoinTokenHash: &tokenHash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return registeredCluster{}, rpcerr.New(connect.CodeUnauthenticated, errEnrollmentRejected)
	}
	if isPgConstraintViolation(err) {
		return registeredCluster{}, rpcerr.New(connect.CodeFailedPrecondition, errClusterKeyEnrolled)
	}
	if err != nil {
		return registeredCluster{}, fmt.Errorf("enroll cluster: %w", err)
	}
	if err := notifyCluster(ctx, qtx, row.ID); err != nil {
		return registeredCluster{}, err
	}
	return registeredCluster{id: row.ID, name: row.Name, region: row.Region}, nil
}

func registeredAs(row genDb.GetEnrolledClusterRow, key uuid.UUID) (registeredCluster, error) {
	if row.ClusterKey == nil || *row.ClusterKey != key {
		return registeredCluster{}, rpcerr.New(connect.CodeFailedPrecondition, errClusterKeyMismatch)
	}
	return registeredCluster{id: row.ID, name: row.Name, region: row.Region}, nil
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
		Domains:               reportedDomains(heartbeat.GetDomains()),
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update heartbeat", "error", err, "cluster_id", clusterID)
	}
}

func reportedDomains(domains []string) []string {
	reported := append([]string{}, domains...)
	slices.Sort(reported)
	return slices.Compact(reported)
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
