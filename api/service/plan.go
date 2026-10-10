package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	"github.com/team-loco/loco/internal/locofile"
)

// PlanServer implements the PlanService: it diffs a loco.yaml file against an environment.
type PlanServer struct {
	planv1connect.UnimplementedPlanServiceHandler
	queries      genDb.Querier
	authz        *authz.Authorizer
	resolver     ImageResolver
	registryHost string
	defaults     servicedefaults.Defaults
}

func NewPlanServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	resolver ImageResolver,
	registryHost string,
	defaults servicedefaults.Defaults,
) *PlanServer {
	return &PlanServer{
		queries:      queries,
		authz:        authz.New(db, queries),
		resolver:     resolver,
		registryHost: registryHost,
		defaults:     defaults,
	}
}

// plannedFile is a loco.yaml file planned against one environment: the parsed file, the
// environment, the services the environment runs and the plan.
type plannedFile struct {
	file            *locofile.File
	env             genDb.Environment
	live            liveEnvironment
	platformDomains []genDb.PlatformDomain
	plan            configplan.Plan
}

// liveEnvironment is every service of the workspace as the environment runs it, with the rows
// the apply writes against.
type liveEnvironment struct {
	services    []configplan.Service
	resources   map[string]genDb.Resource
	domains     map[uuid.UUID][]genDb.ResourceDomain
	deployments map[uuid.UUID][]genDb.Deployment
	builds      map[uuid.UUID]genDb.ListLatestSucceededBuildsForResourcesRow
}

// Plan returns the operations an apply of the file would perform in the environment. It writes
// nothing and needs workspace read.
func (s *PlanServer) Plan(
	ctx context.Context,
	req *connect.Request[planv1.PlanRequest],
) (*connect.Response[planv1.PlanResponse], error) {
	r := req.Msg
	planned, err := s.loadPlan(ctx, r.GetFile(), r.GetEnvironmentId(), actions.Plan)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.PlanResponse{
		Revision:   planned.env.Revision,
		Operations: planOperationsToProto(planned.plan.Operations),
		Errors:     planErrorsToProto(planned.plan.Errors),
	}), nil
}

func (s *PlanServer) loadPlan(
	ctx context.Context,
	rawFile []byte,
	rawEnvironmentID string,
	action actions.Action,
) (*plannedFile, error) {
	file, err := locofile.Parse(rawFile)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	environmentID := uuid.MustParse(rawEnvironmentID)
	env, err := s.queries.GetEnvironmentByID(ctx, environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get environment", "error", err, "environmentId", environmentID)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}
	workspaceAction := actions.New(action, env.WorkspaceID.String())
	if authErr := s.authz.Check(ctx, scopes, workspaceAction); authErr != nil {
		slog.WarnContext(ctx, "unauthorized to plan", "workspaceId", env.WorkspaceID)
		return nil, connect.NewError(connect.CodePermissionDenied, authErr)
	}

	services, err := locofile.Resolve(file, env.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	environments, err := s.queries.ListWorkspaceEnvironments(ctx, env.WorkspaceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list environments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	clusters, err := eligibleClusters(ctx, s.queries, env.EnvironmentType)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list clusters", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	platformDomains, err := s.queries.ListActivePlatformDomains(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list platform domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	live, err := s.liveEnvironment(ctx, env)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load the environment's services", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	environmentNames := make([]string, 0, len(environments))
	for _, environment := range environments {
		environmentNames = append(environmentNames, environment.Name)
	}
	regions := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		regions = append(regions, cluster.Region)
	}
	platformDomainNames := make([]string, 0, len(platformDomains))
	for _, platformDomain := range platformDomains {
		platformDomainNames = append(platformDomainNames, platformDomain.Domain)
	}

	plan, err := configplan.Compute(configplan.Input{
		Partial:          file.Partial,
		Services:         services,
		FileEnvironments: fileEnvironments(file),
		Environments:     environmentNames,
		Regions:          regions,
		PlatformDomains:  platformDomainNames,
		Live:             live.services,
		Defaults:         s.defaults,
		Images:           s.resolveImages(ctx, services),
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to compute plan", "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &plannedFile{file: file, env: env, live: live, platformDomains: platformDomains, plan: plan}, nil
}

func fileEnvironments(file *locofile.File) []string {
	names := map[string]struct{}{}
	for _, service := range file.Services {
		for env := range service.Environments {
			names[env] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(names))
}

func (s *PlanServer) resolveImages(
	ctx context.Context,
	services map[string]locofile.Service,
) map[string]configplan.ImageResult {
	images := map[string]configplan.ImageResult{}
	for _, service := range services {
		if service.Image == "" {
			continue
		}
		if _, done := images[service.Image]; done {
			continue
		}
		src := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: service.Image}
		pinned, err := pinPublicImage(ctx, s.resolver, s.registryHost, src)
		if connectErr, isConnect := errors.AsType[*connect.Error](err); isConnect {
			err = connectErr.Unwrap()
		}
		if err != nil {
			images[service.Image] = configplan.ImageResult{Err: err}
			continue
		}
		images[service.Image] = configplan.ImageResult{Pinned: pinned.GetImage()}
	}
	return images
}

func (s *PlanServer) liveEnvironment(ctx context.Context, env genDb.Environment) (liveEnvironment, error) {
	resources, err := s.queries.ListWorkspaceServiceResources(ctx, env.WorkspaceID)
	if err != nil {
		return liveEnvironment{}, fmt.Errorf("list resources: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(resources))
	for _, res := range resources {
		ids = append(ids, res.ID)
	}

	domains, err := s.queries.ListEnvironmentResourceDomains(ctx, genDb.ListEnvironmentResourceDomainsParams{
		ResourceIds:   ids,
		EnvironmentID: env.ID,
	})
	if err != nil {
		return liveEnvironment{}, fmt.Errorf("list domains: %w", err)
	}
	deployments, err := s.queries.ListActiveDeploymentsForEnvironment(ctx, env.ID)
	if err != nil {
		return liveEnvironment{}, fmt.Errorf("list deployments: %w", err)
	}
	latestBuilds, err := s.queries.ListLatestSucceededBuildsForResources(ctx, ids)
	if err != nil {
		return liveEnvironment{}, fmt.Errorf("list latest builds: %w", err)
	}

	live := liveEnvironment{
		services:    make([]configplan.Service, 0, len(resources)),
		resources:   make(map[string]genDb.Resource, len(resources)),
		domains:     map[uuid.UUID][]genDb.ResourceDomain{},
		deployments: map[uuid.UUID][]genDb.Deployment{},
		builds:      map[uuid.UUID]genDb.ListLatestSucceededBuildsForResourcesRow{},
	}
	for _, domain := range domains {
		live.domains[domain.ResourceID] = append(live.domains[domain.ResourceID], domain)
	}
	for _, deployment := range deployments {
		live.deployments[deployment.ResourceID] = append(live.deployments[deployment.ResourceID], deployment)
	}
	for _, build := range latestBuilds {
		live.builds[build.ResourceID] = build
	}

	for _, res := range resources {
		latest, built := live.builds[res.ID]
		state, stateErr := s.liveState(ctx, res, live.domains[res.ID], live.deployments[res.ID])
		if stateErr != nil {
			return liveEnvironment{}, fmt.Errorf("resource %s: %w", res.Name, stateErr)
		}
		if built && state.Image == "" && state.Dockerfile == "" {
			state.Dockerfile = latest.DockerfilePath
			state.Context = latest.Context
		}
		live.resources[res.Name] = res
		live.services = append(live.services, configplan.Service{
			Name:    res.Name,
			Partial: derefString(res.Partial),
			Built:   built,
			State:   state,
		})
	}
	return live, nil
}

func (s *PlanServer) liveState(
	ctx context.Context,
	res genDb.Resource,
	domains []genDb.ResourceDomain,
	deployments []genDb.Deployment,
) (configplan.State, error) {
	resourceSpec, err := converter.DeserializeResourceSpec(res.Spec, res.Type)
	if err != nil {
		return configplan.State{}, fmt.Errorf("resource spec: %w", err)
	}
	spec := resourceSpec.GetService()

	state := configplan.State{
		Port:    spec.GetRouting().GetPort(),
		Health:  s.liveHealth(spec.GetHealthCheck()),
		Regions: make(map[string]configplan.Region, len(spec.GetRegions())),
	}
	if spec.GetRouting() != nil {
		state.Routing = &configplan.Routing{
			PathPrefix:  converter.FirstSet(spec.GetRouting().GetPathPrefix(), s.defaults.PathPrefix),
			IdleTimeout: converter.FirstSet(spec.GetRouting().GetIdleTimeout(), s.defaults.IdleTimeout),
		}
	}
	slices.SortStableFunc(domains, func(a, b genDb.ResourceDomain) int {
		return boolCompare(b.IsPrimary, a.IsPrimary)
	})
	for _, domain := range domains {
		state.Domains = append(state.Domains, domain.Domain)
	}
	for region, target := range spec.GetRegions() {
		if !target.GetEnabled() {
			continue
		}
		state.Regions[region] = configplan.Region{
			CPU:         target.GetCpu(),
			Memory:      target.GetMemory(),
			MinReplicas: target.GetMinReplicas(),
			MaxReplicas: target.GetMaxReplicas(),
			Autoscaling: liveAutoscaling(target.GetScalers()),
		}
	}

	seenRegions := map[string]bool{}
	for index, deployment := range deployments {
		if seenRegions[deployment.Region] {
			continue
		}
		seenRegions[deployment.Region] = true
		deploymentSpec, specErr := converter.DeserializeDeploymentSpec(deployment.Spec, string(res.Type))
		if specErr != nil {
			return configplan.State{}, fmt.Errorf("deployment %s spec: %w", deployment.ID, specErr)
		}
		service := deploymentSpec.GetService()
		region := state.Regions[deployment.Region]
		region.CPU = converter.FirstSet(service.GetCpu(), region.CPU)
		region.Memory = converter.FirstSet(service.GetMemory(), region.Memory)
		region.MinReplicas = converter.FirstSet(service.GetMinReplicas(), region.MinReplicas)
		region.MaxReplicas = converter.FirstSet(service.GetMaxReplicas(), region.MaxReplicas)
		if service.GetScalers() != nil {
			region.Autoscaling = liveAutoscaling(service.GetScalers())
		}
		state.Regions[deployment.Region] = region
		if index > 0 {
			continue
		}
		state.Port = converter.FirstSet(service.GetPort(), state.Port)
		if service.GetHealthCheck() != nil {
			state.Health = s.liveHealth(service.GetHealthCheck())
		}
		build := service.GetBuild()
		switch build.GetType() {
		case buildSourceTypeImage:
			state.Image = build.GetImage()
		case buildSourceTypeDockerfile:
			deployed, buildErr := s.deployedBuild(ctx, build)
			if buildErr != nil {
				return configplan.State{}, fmt.Errorf("deployment %s build: %w", deployment.ID, buildErr)
			}
			state.Dockerfile = deployed.DockerfilePath
			state.Context = deployed.Context
		}
		env, envErr := desiredEnv(ctx, s.queries, res.ID, deployment.ClusterID)
		if envErr != nil {
			return configplan.State{}, fmt.Errorf("deployment %s env: %w", deployment.ID, envErr)
		}
		state.Env = env
	}
	if len(deployments) > 0 {
		maps.DeleteFunc(state.Regions, func(region string, _ configplan.Region) bool {
			return !seenRegions[region]
		})
	}
	return state, nil
}

func (s *PlanServer) deployedBuild(ctx context.Context, build *deploymentv1.BuildSource) (genDb.Build, error) {
	buildID, err := uuid.Parse(build.GetBuildId())
	if err != nil {
		return genDb.Build{}, fmt.Errorf("parse build id %q: %w", build.GetBuildId(), err)
	}
	deployed, err := s.queries.GetBuildByID(ctx, buildID)
	if err != nil {
		return genDb.Build{}, fmt.Errorf("get build %s: %w", buildID, err)
	}
	return deployed, nil
}

func (s *PlanServer) liveHealth(health *deploymentv1.HealthCheckConfig) configplan.Health {
	if health == nil {
		return configplan.DefaultHealth(s.defaults)
	}
	return configplan.Health{
		Path:               health.GetPath(),
		Interval:           health.GetIntervalSeconds(),
		Timeout:            health.GetTimeoutSeconds(),
		FailThreshold:      health.GetFailureThreshold(),
		StartupGracePeriod: health.GetInitialDelaySeconds(),
	}
}

func liveAutoscaling(scalers *deploymentv1.Scalers) *configplan.Autoscaling {
	if !scalers.GetEnabled() {
		return nil
	}
	return &configplan.Autoscaling{
		CPUTarget:    scalers.GetCpuTarget(),
		MemoryTarget: scalers.GetMemoryTarget(),
	}
}

func boolCompare(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}

func planOperationsToProto(operations []configplan.Operation) []*planv1.PlanOperation {
	result := make([]*planv1.PlanOperation, 0, len(operations))
	for _, op := range operations {
		changes := make([]*planv1.FieldChange, 0, len(op.Changes))
		for _, change := range op.Changes {
			changes = append(changes, &planv1.FieldChange{
				Path:   change.Path,
				Before: change.Before,
				After:  change.After,
			})
		}
		result = append(result, &planv1.PlanOperation{
			Kind:        planKindToProto(op.Kind),
			Service:     op.Service,
			Changes:     changes,
			Destructive: op.Destructive,
			NeedsDeploy: op.NeedsDeploy,
		})
	}
	return result
}

func planKindToProto(kind configplan.Kind) planv1.PlanOperationKind {
	switch kind {
	case configplan.KindCreate:
		return planv1.PlanOperationKind_PLAN_OPERATION_KIND_CREATE
	case configplan.KindUpdate:
		return planv1.PlanOperationKind_PLAN_OPERATION_KIND_UPDATE
	case configplan.KindDelete:
		return planv1.PlanOperationKind_PLAN_OPERATION_KIND_DELETE
	case configplan.KindImport:
		return planv1.PlanOperationKind_PLAN_OPERATION_KIND_IMPORT
	default:
		return planv1.PlanOperationKind_PLAN_OPERATION_KIND_UNSPECIFIED
	}
}

func planErrorsToProto(planErrors []configplan.Error) []*planv1.PlanError {
	result := make([]*planv1.PlanError, 0, len(planErrors))
	for _, planErr := range planErrors {
		result = append(result, &planv1.PlanError{
			Service: planErr.Service,
			Path:    planErr.Path,
			Message: planErr.Err.Error(),
		})
	}
	return result
}
