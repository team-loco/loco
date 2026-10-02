package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/commandbus"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
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
	commandBus *commandbus.Bus
}

// NewAgentServer creates a new AgentServer instance.
func NewAgentServer(db *pgxpool.Pool, queries genDb.Querier, commandBus *commandbus.Bus) *AgentServer {
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

	listener, err := s.commandBus.Listen(ctx, cluster.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to listen for agent commands", "error", err, "cluster_id", cluster.ID)
		return connect.NewError(connect.CodeUnavailable, errors.New("command queue unavailable"))
	}
	defer listener.Close()

	errCh := make(chan error, 1)
	go s.receiveAcks(ctx, stream, cluster.ID, errCh)

	for {
		if err := s.deliverCommands(ctx, stream, cluster.ID); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "command stream context done", "cluster_id", cluster.ID)
			return ctx.Err()

		case err := <-errCh:
			slog.InfoContext(ctx, "command stream closed", "cluster_id", cluster.ID, "error", err)
			return err

		case err := <-listener.Err():
			slog.ErrorContext(ctx, "command listener failed", "cluster_id", cluster.ID, "error", err)
			return connect.NewError(connect.CodeUnavailable, errors.New("command queue unavailable"))

		case <-listener.Wake():
		}
	}
}

func (s *AgentServer) receiveAcks(
	ctx context.Context,
	stream *connect.BidiStream[agentv1.CommandStreamRequest, agentv1.CommandStreamResponse],
	clusterID uuid.UUID,
	errCh chan<- error,
) {
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

		commandID, err := uuid.Parse(ack.GetCommandId())
		if err != nil {
			slog.WarnContext(ctx, "ack with invalid command id",
				"command_id", ack.GetCommandId(),
				"cluster_id", clusterID,
			)
			continue
		}

		if ack.GetSuccess() {
			if err := s.commandBus.Ack(ctx, clusterID, commandID); err != nil {
				slog.ErrorContext(ctx, "failed to ack command", "command_id", commandID, "error", err)
			}
			continue
		}

		slog.WarnContext(ctx, "command failed",
			"command_id", commandID,
			"error", ack.GetErrorMessage(),
			"retry", ack.GetRetry(),
		)
		if err := s.commandBus.Nack(ctx, clusterID, commandID, ack.GetRetry(), ack.GetErrorMessage()); err != nil {
			slog.ErrorContext(ctx, "failed to nack command", "command_id", commandID, "error", err)
		}
	}
}

func (s *AgentServer) deliverCommands(
	ctx context.Context,
	stream *connect.BidiStream[agentv1.CommandStreamRequest, agentv1.CommandStreamResponse],
	clusterID uuid.UUID,
) error {
	for {
		cmds, err := s.commandBus.Claim(ctx, clusterID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to claim commands", "error", err, "cluster_id", clusterID)
			return nil
		}
		if len(cmds) == 0 {
			return nil
		}

		for i := range cmds {
			cmd := &cmds[i]
			protoCmd, err := commandToProto(cmd)
			if err != nil {
				slog.ErrorContext(ctx, "failed to convert command", "error", err, "command_id", cmd.ID)
				errMessage := err.Error()
				if nackErr := s.commandBus.Nack(ctx, clusterID, cmd.ID, false, errMessage); nackErr != nil {
					slog.ErrorContext(ctx, "failed to nack command", "command_id", cmd.ID, "error", nackErr)
				}
				continue
			}
			if err := stream.Send(protoCmd); err != nil {
				slog.ErrorContext(ctx, "failed to send command", "error", err, "command_id", cmd.ID)
				return err
			}
			slog.InfoContext(ctx, "command delivered",
				"command_id", cmd.ID,
				"cluster_id", clusterID,
				"type", cmd.Type,
				"attempt", cmd.Attempts,
			)
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

	updated, err := s.queries.UpdateDeploymentStatusFromAgent(ctx, genDb.UpdateDeploymentStatusFromAgentParams{
		Status:    dbStatus,
		Message:   r.GetMessage(),
		ID:        deploymentIDParsed,
		ClusterID: cluster.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update deployment status", "error", err, "deployment_id", r.GetDeploymentId())
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if updated == 0 {
		return nil, connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	slog.InfoContext(ctx, "deployment status updated",
		"deployment_id", r.GetDeploymentId(),
		"resource_id", r.GetResourceId(),
		"phase", r.GetPhase().String(),
		"message", r.GetMessage(),
	)

	return connect.NewResponse(&agentv1.ReportStatusResponse{}), nil
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
func commandToProto(cmd *commandbus.Command) (*agentv1.CommandStreamResponse, error) {
	createdAt := timestamppb.New(cmd.CreatedAt)
	protoCmd := &agentv1.CommandStreamResponse{
		CommandId: cmd.ID.String(),
		ClusterId: cmd.ClusterID.String(),
		CreatedAt: createdAt,
	}

	switch cmd.Type {
	case commandbus.CommandTypeDeploy:
		if len(cmd.Payload) == 0 {
			return nil, errors.New("deploy command has no payload")
		}
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
		var payload DeleteCommandPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("unmarshal delete payload: %w", err)
		}
		protoCmd.Payload = &agentv1.CommandStreamResponse_Delete{
			Delete: &agentv1.DeleteCommand{ResourceId: payload.ResourceID},
		}
	default:
		return nil, fmt.Errorf("unknown command type %q", cmd.Type)
	}

	return protoCmd, nil
}
