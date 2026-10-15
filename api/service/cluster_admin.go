package service

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var errClusterAdmin = errors.New("cluster administration failed")

type ClusterAdminServer struct {
	pool    *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
}

func NewClusterAdminServer(pool *pgxpool.Pool, queries genDb.Querier) *ClusterAdminServer {
	return &ClusterAdminServer{pool: pool, queries: queries, authz: authz.New(pool, queries)}
}

func (s *ClusterAdminServer) authorize(ctx context.Context) error {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, publicPermissionDenied)
	}
	if err := s.authz.Check(ctx, scopes, actions.NewSystem(actions.ManageClusters)); err != nil {
		return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	return nil
}

func (s *ClusterAdminServer) ListClusters(
	ctx context.Context,
	_ *clusterv1.ListClustersRequest,
) (*clusterv1.ListClustersResponse, error) {
	if err := s.authorize(ctx); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListAdminClusters(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "list clusters failed", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errClusterAdmin)
	}
	result := &clusterv1.ListClustersResponse{}
	for _, row := range rows {
		cluster := &clusterv1.Cluster{
			Id: row.ID.String(), Name: row.Name, Region: row.Region, Tier: row.Tier,
			IsActive: row.IsActive, RemovedAt: timeutil.ParsePostgresTimestampPtr(row.RemovedAt),
			LastHeartbeat: timeutil.ParsePostgresTimestampPtr(row.LastHeartbeat),
			HealthStatus:  row.HealthStatus, AgentVersion: row.AgentVersion,
			ConnectionCredentialFingerprint: row.ConnectionCredentialFingerprint,
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
	operation, id string,
	force bool,
) (ClusterOperationResult, error) {
	if err := s.authorize(ctx); err != nil {
		return ClusterOperationResult{}, err
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ClusterOperationResult{}, rpcerr.New(connect.CodeInvalidArgument, err)
	}
	result, err := ManageCluster(ctx, s.pool, operation, parsed, force)
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
			detail, detailErr := connectproto.NewErrorDetail(
				&clusterv1.ClusterWorkloads{Workloads: clusterWorkloads(result.Workloads)},
			)
			if detailErr != nil {
				return result, refused
			}
			refused = refused.WithDetail(detail)
		}
		return result, refused
	}
	slog.ErrorContext(ctx, "manage cluster failed", "cluster_id", id, "operation", operation, "error", err)
	return result, rpcerr.New(connect.CodeInternal, errClusterAdmin)
}

func (s *ClusterAdminServer) PauseCluster(
	ctx context.Context,
	req *clusterv1.PauseClusterRequest,
) (*clusterv1.PauseClusterResponse, error) {
	result, err := s.manage(ctx, ClusterPause, req.GetClusterId(), false)
	if err != nil {
		return nil, err
	}
	return &clusterv1.PauseClusterResponse{ClusterId: result.ClusterID.String()}, nil
}

func (s *ClusterAdminServer) ResumeCluster(
	ctx context.Context,
	req *clusterv1.ResumeClusterRequest,
) (*clusterv1.ResumeClusterResponse, error) {
	result, err := s.manage(ctx, ClusterResume, req.GetClusterId(), false)
	if err != nil {
		return nil, err
	}
	return &clusterv1.ResumeClusterResponse{ClusterId: result.ClusterID.String()}, nil
}

func (s *ClusterAdminServer) RemoveCluster(
	ctx context.Context,
	req *clusterv1.RemoveClusterRequest,
) (*clusterv1.RemoveClusterResponse, error) {
	result, err := s.manage(ctx, ClusterRemove, req.GetClusterId(), req.GetForce())
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
	result, err := s.manage(ctx, ClusterRestore, req.GetClusterId(), false)
	if err != nil {
		return nil, err
	}
	return &clusterv1.RestoreClusterResponse{ClusterId: result.ClusterID.String()}, nil
}
