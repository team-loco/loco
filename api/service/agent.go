package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/commandbus"
	agentv1 "github.com/team-loco/loco/proto/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/proto/loco/deployment/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrAgentNotAuthenticated = errors.New("agent not authenticated")
	ErrInvalidAgentToken     = errors.New("invalid agent token")
)

// AgentServer implements the AgentService for agent communication.
type AgentServer struct {
	db         *pgxpool.Pool
	queries    genDb.Querier
	commandBus commandbus.CommandBus
}

// NewAgentServer creates a new AgentServer instance.
func NewAgentServer(db *pgxpool.Pool, queries genDb.Querier, commandBus commandbus.CommandBus) *AgentServer {
	return &AgentServer{
		db:         db,
		queries:    queries,
		commandBus: commandBus,
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
func (s *AgentServer) authenticateAgent(ctx context.Context, authHeader string) (genDb.GetClusterByAgentTokenRow, error) {
	token := extractBearerToken(authHeader)
	if token == "" {
		return genDb.GetClusterByAgentTokenRow{}, ErrInvalidAgentToken
	}

	tokenHash := hashToken(token)
	cluster, err := s.queries.GetClusterByAgentToken(ctx, pgtype.Text{String: tokenHash, Valid: true})
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
	err = s.queries.UpdateClusterAgentInfo(ctx, genDb.UpdateClusterAgentInfoParams{
		ID:           cluster.ID,
		AgentVersion: pgtype.Text{String: r.GetAgentVersion(), Valid: true},
		CapacityCpuMillicores: pgtype.Int8{
			Int64: r.GetCapacity().GetCpuMillicoresTotal(),
			Valid: r.GetCapacity() != nil,
		},
		CapacityMemoryBytes: pgtype.Int8{
			Int64: r.GetCapacity().GetMemoryBytesTotal(),
			Valid: r.GetCapacity() != nil,
		},
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

// CommandStream handles bidirectional command streaming.
// Control plane sends commands, agent sends acks back.
func (s *AgentServer) CommandStream(
	ctx context.Context,
	stream *connect.BidiStream[agentv1.CommandStreamRequest, agentv1.CommandStreamResponse],
) error {
	// Authenticate
	cluster, err := s.authenticateAgent(ctx, stream.RequestHeader().Get("Authorization"))
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}

	slog.InfoContext(ctx, "command stream opened", "cluster_id", cluster.ID)

	// Register this agent's command channel
	cmdChan, err := s.commandBus.Receive(ctx, cluster.ID.String())
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	// Handle incoming acks in separate goroutine
	errCh := make(chan error, 1)
	go func() {
		for {
			ack, err := stream.Receive()
			if err != nil {
				if errors.Is(err, io.EOF) {
					errCh <- nil
				} else {
					errCh <- err
				}
				return
			}

			if ack.GetSuccess() {
				if err := s.commandBus.Ack(ctx, ack.GetCommandId()); err != nil {
					slog.WarnContext(ctx, "failed to ack command", "command_id", ack.GetCommandId(), "error", err)
				}
			} else {
				if err := s.commandBus.Nack(ctx, ack.GetCommandId(), ack.GetRetry()); err != nil {
					slog.WarnContext(ctx, "failed to nack command", "command_id", ack.GetCommandId(), "error", err)
				}
				slog.WarnContext(ctx, "command failed",
					"command_id", ack.GetCommandId(),
					"error", ack.GetErrorMessage(),
					"retry", ack.GetRetry(),
				)
			}
		}
	}()

	// Send commands to agent
	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "command stream context done", "cluster_id", cluster.ID)
			return ctx.Err()

		case err := <-errCh:
			slog.InfoContext(ctx, "command stream closed", "cluster_id", cluster.ID, "error", err)
			return err

		case cmd, ok := <-cmdChan:
			if !ok {
				slog.InfoContext(ctx, "command channel closed", "cluster_id", cluster.ID)
				return nil
			}

			protoCmd := commandToProto(cmd)
			if err := stream.Send(protoCmd); err != nil {
				slog.ErrorContext(ctx, "failed to send command", "error", err, "command_id", cmd.ID)
				return err
			}
		}
	}
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
		err = s.queries.UpdateClusterHeartbeat(ctx, genDb.UpdateClusterHeartbeatParams{
			ID: cluster.ID,
			LastHeartbeat: pgtype.Timestamptz{
				Time:  time.Now(),
				Valid: true,
			},
			CapacityCpuMillicores: pgtype.Int8{
				Int64: req.GetCapacity().GetCpuMillicoresTotal(),
				Valid: req.GetCapacity() != nil,
			},
			CapacityMemoryBytes: pgtype.Int8{
				Int64: req.GetCapacity().GetMemoryBytesTotal(),
				Valid: req.GetCapacity() != nil,
			},
			HealthStatus: pgtype.Text{
				String: healthStatusFromProto(req.GetHealth()),
				Valid:  true,
			},
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to update heartbeat", "error", err, "cluster_id", cluster.ID)
		}

		// Check if we have a directive to send
		directive := s.getDirectiveForCluster(ctx, cluster.ID)
		if directive != nil {
			if err := stream.Send(directive); err != nil {
				return err
			}
		}
		// Otherwise: silence means "all good"
	}
}

// ReportStatus handles deployment status reports from agents.
func (s *AgentServer) ReportStatus(
	ctx context.Context,
	req *connect.Request[agentv1.ReportStatusRequest],
) (*connect.Response[agentv1.ReportStatusResponse], error) {
	r := req.Msg

	// Authenticate
	cluster, err := s.authenticateAgent(ctx, req.Header().Get("Authorization"))
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	// Verify this cluster owns the deployment
	if r.GetClusterId() != cluster.ID.String() {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("cluster ID mismatch"))
	}

	// Parse deployment ID
	deploymentIDParsed, err := uuid.Parse(r.GetDeploymentId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid deployment ID: %w", err))
	}

	// Map proto phase to DB status
	dbStatus := protoPhaseToDBStatus(r.GetPhase())

	// Update deployment status
	err = s.queries.UpdateDeploymentStatusWithMessage(ctx, genDb.UpdateDeploymentStatusWithMessageParams{
		ID:      deploymentIDParsed,
		Status:  dbStatus,
		Message: r.GetMessage(),
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update deployment status", "error", err, "deployment_id", r.GetDeploymentId())
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	slog.InfoContext(ctx, "deployment status updated",
		"deployment_id", r.GetDeploymentId(),
		"resource_id", r.GetResourceId(),
		"phase", r.GetPhase().String(),
		"message", r.GetMessage(),
	)

	return connect.NewResponse(&agentv1.ReportStatusResponse{}), nil
}

// getDirectiveForCluster checks for pending directives for a cluster.
// Returns nil if no directive is pending.
func (s *AgentServer) getDirectiveForCluster(ctx context.Context, clusterID uuid.UUID) *agentv1.HeartbeatResponse {
	// TODO: implement directive storage (e.g., in cache or DB)
	// For now, no directives
	return nil
}

// healthStatusFromProto converts agent health to a status string.
func healthStatusFromProto(h *agentv1.AgentHealth) string {
	if h == nil {
		return "unknown"
	}
	if h.GetKubernetesHealthy() && h.GetControllerHealthy() {
		return "healthy"
	}
	if !h.GetKubernetesHealthy() || !h.GetControllerHealthy() {
		return "unhealthy"
	}
	return "degraded"
}

// protoPhaseToDBStatus converts proto deployment phase to DB status.
func protoPhaseToDBStatus(phase deploymentv1.DeploymentPhase) genDb.DeploymentStatus {
	switch phase {
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_PENDING:
		return genDb.DeploymentStatusPending
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_DEPLOYING:
		return genDb.DeploymentStatusDeploying
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING:
		return genDb.DeploymentStatusRunning
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_SUCCEEDED:
		return genDb.DeploymentStatusSucceeded
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED:
		return genDb.DeploymentStatusFailed
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_CANCELED:
		return genDb.DeploymentStatusCanceled
	default:
		return genDb.DeploymentStatusPending
	}
}

// commandToProto converts a commandbus.Command to a proto CommandStreamResponse.
func commandToProto(cmd *commandbus.Command) *agentv1.CommandStreamResponse {
	protoCmd := &agentv1.CommandStreamResponse{
		CommandId: cmd.ID,
		ClusterId: cmd.ClusterID,
		CreatedAt: timestamppb.New(cmd.CreatedAt),
	}

	switch cmd.Type {
	case commandbus.CommandTypeDeploy:
		protoCmd.Type = agentv1.CommandType_COMMAND_TYPE_DEPLOY
		// Payload is already JSON, unmarshal to proto would go here
		// For now, we embed it in DeployCommand.ApplicationSpec
		protoCmd.Payload = &agentv1.CommandStreamResponse_Deploy{
			Deploy: &agentv1.DeployCommand{
				ApplicationSpec: cmd.Payload,
			},
		}
	case commandbus.CommandTypeDelete:
		protoCmd.Type = agentv1.CommandType_COMMAND_TYPE_DELETE
		protoCmd.Payload = &agentv1.CommandStreamResponse_Delete{
			Delete: &agentv1.DeleteCommand{},
		}
	case commandbus.CommandTypeScale:
		protoCmd.Type = agentv1.CommandType_COMMAND_TYPE_SCALE
		protoCmd.Payload = &agentv1.CommandStreamResponse_Scale{
			Scale: &agentv1.ScaleCommand{},
		}
	case commandbus.CommandTypeUpdateEnv:
		protoCmd.Type = agentv1.CommandType_COMMAND_TYPE_UPDATE_ENV
		protoCmd.Payload = &agentv1.CommandStreamResponse_UpdateEnv{
			UpdateEnv: &agentv1.UpdateEnvCommand{},
		}
	}

	return protoCmd
}
