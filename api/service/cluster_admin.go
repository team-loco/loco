package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/rpcerr"
)

const joinTokenPrefix = "loco_j_"

var (
	errRegistrationChanged = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"cluster registration is removed or its requested fields differ",
	)
	errInvalidClusterFields = errors.New(
		"cluster name and region are required and tier must be staging or production",
	)
)

type ClusterAdminServer struct {
	pool         *pgxpool.Pool
	queries      genDb.Querier
	authz        *authz.Authorizer
	joinTokenTTL time.Duration
}

func NewClusterAdminServer(pool *pgxpool.Pool, queries genDb.Querier, joinTokenTTL time.Duration) *ClusterAdminServer {
	return &ClusterAdminServer{
		pool:         pool,
		queries:      queries,
		authz:        authz.New(pool, queries),
		joinTokenTTL: joinTokenTTL,
	}
}

type joinToken struct {
	token     string
	hash      string
	expiresAt time.Time
}

func (s *ClusterAdminServer) newJoinToken() joinToken {
	token := joinTokenPrefix + rand.Text()
	return joinToken{token: token, hash: bearer.Hash(token), expiresAt: time.Now().Add(s.joinTokenTTL)}
}

func (t joinToken) proto() *clusterv1.JoinToken {
	expiresAt := timestamppb.New(t.expiresAt)
	return &clusterv1.JoinToken{Token: t.token, ExpiresAt: expiresAt}
}

func (s *ClusterAdminServer) CreateCluster(
	ctx context.Context,
	req *clusterv1.CreateClusterRequest,
) (*clusterv1.CreateClusterResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ManageClusters); err != nil {
		return nil, err
	}
	if err := protovalidate.Validate(req); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errInvalidClusterFields.Error()).WithCause(err)
	}
	requestID, err := uuid.Parse(req.GetRequestId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, errInvalidClusterFields)
	}
	tier := protoEnvTypeToString(req.GetTier())
	response := &clusterv1.CreateClusterResponse{}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := genDb.New(tx)
		if createErr := queries.CreateCluster(ctx, genDb.CreateClusterParams{
			Name: req.GetName(), Region: req.GetRegion(), Tier: tier, ID: requestID,
			ApplicationsEnabled: req.GetApplicationsEnabled(), BuildsAllowed: req.GetBuildsAllowed(),
		}); createErr != nil {
			return fmt.Errorf("insert cluster: %w", createErr)
		}
		row, lookupErr := queries.LockClusterRegistration(ctx, requestID)
		if lookupErr != nil {
			return fmt.Errorf("lock cluster registration: %w", lookupErr)
		}
		if row.RemovedAt != nil || row.Name != req.GetName() || row.Region != req.GetRegion() || row.Tier != tier {
			return errRegistrationChanged
		}
		response.ClusterId = row.ID.String()
		if row.AgentPublicKey != nil {
			return nil
		}
		token := s.newJoinToken()
		if _, issueErr := queries.SetClusterJoinToken(ctx, genDb.SetClusterJoinTokenParams{
			ID: row.ID, JoinTokenHash: &token.hash, JoinTokenExpiresAt: &token.expiresAt,
		}); issueErr != nil {
			return fmt.Errorf("set join token: %w", issueErr)
		}
		response.JoinToken = token.proto()
		return nil
	})
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "cluster registration ready", "cluster_id", response.GetClusterId())
	return response, nil
}

func (s *ClusterAdminServer) IssueJoinToken(
	ctx context.Context,
	req *clusterv1.IssueJoinTokenRequest,
) (*clusterv1.IssueJoinTokenResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ManageClusters); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetClusterId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, err)
	}
	token := s.newJoinToken()
	_, err = s.queries.SetClusterJoinToken(ctx, genDb.SetClusterJoinTokenParams{
		ID:                 id,
		JoinTokenHash:      &token.hash,
		JoinTokenExpiresAt: &token.expiresAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeFailedPrecondition, ErrClusterNotEnrollable)
	}
	if err != nil {
		return nil, fmt.Errorf("set cluster %s join token: %w", id, err)
	}
	slog.InfoContext(ctx, "cluster join token issued", "cluster_id", id)
	return &clusterv1.IssueJoinTokenResponse{ClusterId: id.String(), JoinToken: token.proto()}, nil
}

func (s *ClusterAdminServer) ListClusters(
	ctx context.Context,
	_ *clusterv1.ListClustersRequest,
) (*clusterv1.ListClustersResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ManageClusters); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListAdminClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	result := &clusterv1.ListClustersResponse{}
	for _, row := range rows {
		cluster := &clusterv1.Cluster{
			Id: row.ID.String(), Name: row.Name, Region: row.Region, Tier: stringToProtoEnvType(row.Tier),
			IsActive: row.IsActive, RemovedAt: optionalTimestamp(row.RemovedAt),
			LastHeartbeat: optionalTimestamp(row.LastHeartbeat),
			HealthStatus:  row.HealthStatus, AgentVersion: row.AgentVersion,
			BuildsEnabled:       row.BuildsEnabled,
			ApplicationsEnabled: row.ApplicationsEnabled, BuildsAllowed: row.BuildsAllowed,
		}
		if row.AgentPublicKey != nil {
			fingerprint := sha256.Sum256(row.AgentPublicKey)
			cluster.AgentKeyFingerprint = new(hex.EncodeToString(fingerprint[:]))
		}
		if row.ClusterKey != nil {
			cluster.ClusterKey = row.ClusterKey.String()
		}
		result.Clusters = append(result.Clusters, cluster)
	}
	return result, nil
}

func clusterWorkloads(rows []genDb.ListClusterWorkloadsRow) []*clusterv1.ClusterWorkload {
	workloads := make([]*clusterv1.ClusterWorkload, 0, len(rows))
	for _, row := range rows {
		workloads = append(workloads, &clusterv1.ClusterWorkload{Id: row.ID.String(), Kind: row.Kind})
	}
	return workloads
}

func (s *ClusterAdminServer) manage(
	ctx context.Context,
	operation clusterOperation,
	id string,
	force bool,
) (clusterOperationResult, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ManageClusters); err != nil {
		return clusterOperationResult{}, err
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return clusterOperationResult{}, rpcerr.New(connect.CodeInvalidArgument, err)
	}
	result, err := manageCluster(ctx, s.pool, operation, parsed, force)
	if err == nil {
		return result, nil
	}
	if errors.Is(err, ErrClusterNotFound) {
		return result, rpcerr.New(connect.CodeNotFound, err)
	}
	if errors.Is(err, ErrClusterRemoved) || errors.Is(err, ErrClusterNotRemoved) ||
		errors.Is(err, ErrClusterNotPaused) ||
		errors.Is(err, ErrClusterHasWorkloads) {
		refused := rpcerr.New(connect.CodeFailedPrecondition, err)
		if len(result.Workloads) > 0 {
			workloads := &clusterv1.ClusterWorkloads{Workloads: clusterWorkloads(result.Workloads)}
			refused = withDetail(refused, workloads)
		}
		return result, refused
	}
	return result, fmt.Errorf("%s cluster %s: %w", operation, id, err)
}

func (s *ClusterAdminServer) PauseCluster(
	ctx context.Context,
	req *clusterv1.PauseClusterRequest,
) (*clusterv1.PauseClusterResponse, error) {
	result, err := s.manage(ctx, clusterPause, req.GetClusterId(), false)
	if err != nil {
		return nil, err
	}
	return &clusterv1.PauseClusterResponse{ClusterId: result.ClusterID.String()}, nil
}

func (s *ClusterAdminServer) ResumeCluster(
	ctx context.Context,
	req *clusterv1.ResumeClusterRequest,
) (*clusterv1.ResumeClusterResponse, error) {
	result, err := s.manage(ctx, clusterResume, req.GetClusterId(), false)
	if err != nil {
		return nil, err
	}
	return &clusterv1.ResumeClusterResponse{ClusterId: result.ClusterID.String()}, nil
}

func (s *ClusterAdminServer) RemoveCluster(
	ctx context.Context,
	req *clusterv1.RemoveClusterRequest,
) (*clusterv1.RemoveClusterResponse, error) {
	result, err := s.manage(ctx, clusterRemove, req.GetClusterId(), req.GetForce())
	if err != nil {
		return nil, err
	}
	return &clusterv1.RemoveClusterResponse{
		ClusterId: result.ClusterID.String(),
		Workloads: clusterWorkloads(result.Workloads),
	}, nil
}

func (s *ClusterAdminServer) RestoreCluster(
	ctx context.Context,
	req *clusterv1.RestoreClusterRequest,
) (*clusterv1.RestoreClusterResponse, error) {
	result, err := s.manage(ctx, clusterRestore, req.GetClusterId(), false)
	if err != nil {
		return nil, err
	}
	return &clusterv1.RestoreClusterResponse{ClusterId: result.ClusterID.String()}, nil
}

func (s *ClusterAdminServer) UpdateCluster(
	ctx context.Context,
	req *clusterv1.UpdateClusterRequest,
) (*clusterv1.UpdateClusterResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ManageClusters); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetClusterId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "invalid cluster ID").WithCause(err)
	}
	_, err = s.queries.UpdateClusterCapabilities(ctx, genDb.UpdateClusterCapabilitiesParams{
		ID: id, ApplicationsEnabled: req.GetApplicationsEnabled(), BuildsAllowed: req.GetBuildsAllowed(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrClusterNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update cluster capabilities: %w", err)
	}
	return &clusterv1.UpdateClusterResponse{ClusterId: id.String()}, nil
}
