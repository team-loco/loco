package service

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

const localRegion = "local-1"

var (
	errRegionNotFound = rpcerr.NewCoded(connect.CodeNotFound, "region has no cluster")
	errNewRegionPlace = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"a new region needs a country and a subdivision, or local alone",
	)
)

type RegionAdminServer struct {
	queries genDb.Querier
	authz   *authz.Authorizer
}

func NewRegionAdminServer(queries genDb.Querier) *RegionAdminServer {
	return &RegionAdminServer{queries: queries, authz: authz.New(queries)}
}

func (s *RegionAdminServer) ListRegions(
	ctx context.Context,
	_ *clusterv1.ListRegionsRequest,
) (*clusterv1.ListRegionsResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ManageClusters); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListAdminRegions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list regions: %w", err)
	}
	response := &clusterv1.ListRegionsResponse{Regions: make([]*clusterv1.Region, 0, len(rows))}
	for _, row := range rows {
		response.Regions = append(response.Regions, regionToProto(row))
	}
	return response, nil
}

func regionToProto(row genDb.ListAdminRegionsRow) *clusterv1.Region {
	return &clusterv1.Region{Code: row.Code, ClusterCount: row.ClusterCount}
}

// clusterRegion returns the region a new cluster joins: a region that has a cluster, local-1, or a
// new region built from the request's country and subdivision. fresh reports that no cluster is in
// it yet.
func clusterRegion(
	ctx context.Context,
	qtx *genDb.Queries,
	req *clusterv1.CreateClusterRequest,
) (code string, fresh bool, err error) {
	spec := req.GetNewRegion()
	switch {
	case spec == nil:
		code = req.GetRegion()
	case spec.GetLocal():
		if spec.GetCountry() != "" || spec.GetSubdivision() != "" {
			return "", false, errNewRegionPlace
		}
		code = localRegion
	default:
		if spec.GetCountry() == "" || spec.GetSubdivision() == "" {
			return "", false, errNewRegionPlace
		}
		prefix := regionPrefix(spec)
		n, numberErr := qtx.NextRegionNumber(ctx, prefix)
		if numberErr != nil {
			return "", false, fmt.Errorf("number the region: %w", numberErr)
		}
		return fmt.Sprintf("%s-%d", prefix, n), true, nil
	}
	populated, err := qtx.RegionHasClusters(ctx, code)
	if err != nil {
		return "", false, fmt.Errorf("read region: %w", err)
	}
	if !populated && code != localRegion {
		return "", false, errRegionNotFound.Detail("%s", code)
	}
	return code, !populated, nil
}

func regionPrefix(spec *clusterv1.NewRegion) string {
	return spec.GetCountry() + "-" + spec.GetSubdivision()
}

// sameRegion reports whether code is a region the request asks for.
func sameRegion(code string, req *clusterv1.CreateClusterRequest) bool {
	spec := req.GetNewRegion()
	switch {
	case spec == nil:
		return code == req.GetRegion()
	case spec.GetLocal():
		return code == localRegion
	default:
		n, ok := strings.CutPrefix(code, regionPrefix(spec)+"-")
		return ok && n != "" && strings.Trim(n, "0123456789") == ""
	}
}
