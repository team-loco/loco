package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	registryv1 "github.com/team-loco/loco/gen/go/loco/registry/v1"
)

type RegistryServer struct {
	db                *pgxpool.Pool
	queries           db.Querier
	gitlabURL         string
	gitlabPAT         string
	gitlabProjectID   string
	registryBaseImage string
	httpClient        *http.Client
	machine           *tvm.VendingMachine
	proxyMutex        sync.Mutex
	proxyTokens       map[string]registryToken
}

func NewRegistryServer(
	pool *pgxpool.Pool, queries db.Querier, machine *tvm.VendingMachine,
	gitlabURL, gitlabPAT, gitlabProjectID, registryBaseImage string, httpClient *http.Client,
) *RegistryServer {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &RegistryServer{
		db: pool, queries: queries, machine: machine,
		gitlabURL: gitlabURL, gitlabPAT: gitlabPAT, gitlabProjectID: gitlabProjectID,
		registryBaseImage: strings.TrimSuffix(registryBaseImage, "/"), httpClient: httpClient,
		proxyTokens: make(map[string]registryToken),
	}
}

func (s *RegistryServer) GetImageRepository(
	ctx context.Context, req *connect.Request[registryv1.GetImageRepositoryRequest],
) (*connect.Response[registryv1.GetImageRepositoryResponse], error) {
	r := req.Msg
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]db.EntityScope)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication is required"))
	}
	environmentID, err := uuid.Parse(r.GetEnvironmentId())
	if err != nil || !infraSecretName.MatchString(r.GetStackName()) || !infraSecretName.MatchString(r.GetServiceKey()) {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("valid environment, stack and service key are required"),
		)
	}
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, db.EntityScope{
		EntityType: db.EntityTypeEnvironment, EntityID: environmentID, Scope: db.ScopeWrite,
	}); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	if stackErr := tvm.VerifyStackTarget(ctx, environmentID, r.GetStackName()); stackErr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, stackErr)
	}
	if s.registryBaseImage == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no image repository is configured"))
	}
	virtual := "loco/" + environmentID.String() + "/" + r.GetStackName() + "/" + r.GetServiceKey()
	physical := environmentID.String() + "/" + r.GetStackName() + "." + r.GetServiceKey()
	return connect.NewResponse(&registryv1.GetImageRepositoryResponse{
		Repository:     s.registryBaseImage + "/" + physical,
		PushRepository: virtual,
	}), nil
}
