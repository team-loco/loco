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
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
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
		"cluster needs a region, or a new region's country and subdivision",
	)
	errClusterNameRequired = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"name the cluster; the region already has a cluster",
	)
)

type ClusterAdminServer struct {
	pool              *pgxpool.Pool
	queries           genDb.Querier
	authz             *authz.Authorizer
	joinTokenTTL      time.Duration
	joinTokenMaxTTL   time.Duration
	heartbeatInterval time.Duration
}

func NewClusterAdminServer(
	pool *pgxpool.Pool,
	queries genDb.Querier,
	joinTokenTTL, joinTokenMaxTTL, heartbeatInterval time.Duration,
) *ClusterAdminServer {
	return &ClusterAdminServer{
		pool:              pool,
		queries:           queries,
		authz:             authz.New(queries),
		joinTokenTTL:      joinTokenTTL,
		joinTokenMaxTTL:   joinTokenMaxTTL,
		heartbeatInterval: heartbeatInterval,
	}
}

type joinToken struct {
	token     string
	hash      string
	expiresAt time.Time
}

func (s *ClusterAdminServer) tokenTTL(requested *durationpb.Duration) (time.Duration, error) {
	if requested == nil {
		return s.joinTokenTTL, nil
	}
	if requested.CheckValid() != nil || requested.AsDuration() <= 0 || requested.AsDuration() > s.joinTokenMaxTTL {
		return 0, connect.NewError(
			connect.CodeInvalidArgument,
			"join token ttl must be positive and must not exceed the server maximum",
		)
	}
	return requested.AsDuration(), nil
}

func newJoinToken(ttl time.Duration) joinToken {
	token := joinTokenPrefix + rand.Text()
	return joinToken{token: token, hash: bearer.Hash(token), expiresAt: time.Now().Add(ttl)}
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
	ttl, err := s.tokenTTL(req.GetTtl())
	if err != nil {
		return nil, err
	}
	response := &clusterv1.CreateClusterResponse{}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := genDb.New(tx)
		row, lookupErr := queries.LockClusterRegistration(ctx, requestID)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			row, lookupErr = createClusterRow(ctx, queries, req, requestID)
		}
		if lookupErr != nil {
			return lookupErr
		}
		if !sameRegistration(row, req) {
			return errRegistrationChanged
		}
		response.ClusterId = row.ID.String()
		response.Name = row.Name
		response.Region = row.Region
		if row.AgentPublicKey != nil {
			return nil
		}
		token := newJoinToken(ttl)
		if _, issueErr := queries.SetClusterJoinToken(ctx, genDb.SetClusterJoinTokenParams{
			ID: row.ID, JoinTokenHash: &token.hash, JoinTokenExpiresAt: &token.expiresAt,
		}); issueErr != nil {
			return fmt.Errorf("set join token: %w", issueErr)
		}
		response.JoinToken = token.proto()
		return recordCluster(ctx, queries, events.ClusterTokenIssued, row.ID, nil)
	})
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "cluster registration ready", "cluster_id", response.GetClusterId())
	return response, nil
}

// createClusterRow registers the cluster in its region. A region's first cluster defaults to the
// region code as its name.
func createClusterRow(
	ctx context.Context,
	queries *genDb.Queries,
	req *clusterv1.CreateClusterRequest,
	id uuid.UUID,
) (genDb.LockClusterRegistrationRow, error) {
	region, fresh, err := clusterRegion(ctx, queries, req)
	if err != nil {
		return genDb.LockClusterRegistrationRow{}, err
	}
	name := req.GetName()
	if name == "" && !fresh {
		return genDb.LockClusterRegistrationRow{}, errClusterNameRequired.Detail("%s", region)
	}
	if name == "" {
		name = region
	}
	if err = queries.CreateCluster(ctx, genDb.CreateClusterParams{Name: name, Region: region, ID: id}); err != nil {
		return genDb.LockClusterRegistrationRow{}, fmt.Errorf("insert cluster: %w", err)
	}
	if err = recordCluster(ctx, queries, events.ClusterCreated, id, map[string]any{
		events.FieldName: name, events.FieldRegion: region,
	}); err != nil {
		return genDb.LockClusterRegistrationRow{}, err
	}
	row, err := queries.LockClusterRegistration(ctx, id)
	if err != nil {
		return genDb.LockClusterRegistrationRow{}, fmt.Errorf("lock cluster registration: %w", err)
	}
	return row, nil
}

// sameRegistration reports whether a retried request asks for the cluster already recorded.
func sameRegistration(row genDb.LockClusterRegistrationRow, req *clusterv1.CreateClusterRequest) bool {
	return row.RemovedAt == nil && (req.GetName() == "" || row.Name == req.GetName()) && sameRegion(row.Region, req)
}

func (s *ClusterAdminServer) IssueJoinToken(
	ctx context.Context,
	req *clusterv1.IssueJoinTokenRequest,
) (*clusterv1.IssueJoinTokenResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ManageClusters); err != nil {
		return nil, err
	}
	ttl, err := s.tokenTTL(req.GetTtl())
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.GetClusterId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, err)
	}
	token := newJoinToken(ttl)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := genDb.New(tx)
		_, setErr := queries.SetClusterJoinToken(ctx, genDb.SetClusterJoinTokenParams{
			ID:                 id,
			JoinTokenHash:      &token.hash,
			JoinTokenExpiresAt: &token.expiresAt,
		})
		if setErr != nil {
			return setErr
		}
		return recordCluster(ctx, queries, events.ClusterTokenIssued, id, nil)
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

// recordCluster records an installation-level event about a cluster.
func recordCluster(ctx context.Context, q genDb.Querier, eventType string, id uuid.UUID, data map[string]any) error {
	err := events.Record(ctx, q, events.Event{
		Type: eventType, SubjectType: events.SubjectCluster, SubjectID: &id, Data: data,
	})
	if err != nil {
		return fmt.Errorf("record %s: %w", eventType, err)
	}
	return nil
}

func (s *ClusterAdminServer) ListClusters(
	ctx context.Context,
	_ *clusterv1.ListClustersRequest,
) (*clusterv1.ListClustersResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ListClusters); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListAdminClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	result := &clusterv1.ListClustersResponse{}
	for _, row := range rows {
		cluster := &clusterv1.Cluster{
			Id: row.ID.String(), Name: row.Name, Region: row.Region,
			IsActive: row.IsActive, RemovedAt: optionalTimestamp(row.RemovedAt),
			LastHeartbeat: optionalTimestamp(row.LastHeartbeat),
			SilentSince:   optionalTimestamp(row.SilentSince),
			HealthStatus:  row.HealthStatus, AgentVersion: row.AgentVersion,
			HealthMessage: derefString(row.HealthMessage),
			Role:          clusterRoleToProto(row.Role),
			Domains:       row.Domains,
		}
		if row.CapacityReportedAt != nil {
			cluster.Capacity = &clusterv1.ClusterCapacity{
				CpuMillicoresAllocatable: *row.ReportedCpuMillicores,
				CpuMillicoresRequested:   *row.RequestedCpuMillicores,
				MemoryBytesAllocatable:   *row.ReportedMemoryBytes,
				MemoryBytesRequested:     *row.RequestedMemoryBytes,
				PodsAllocatable:          *row.CapacityPods,
				PodsAssigned:             *row.AssignedPods,
				PodsRunning:              *row.RunningPods,
				ReportedAt:               timestamppb.New(*row.CapacityReportedAt),
				Stale:                    time.Since(*row.CapacityReportedAt) > ClusterSilentAfter(s.heartbeatInterval),
			}
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
