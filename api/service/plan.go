package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/platformdomain"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	"github.com/team-loco/loco/internal/locofile"
)

// PlanServer implements the PlanService: it diffs a loco.yaml file against an environment.
type PlanServer struct {
	planv1connect.UnimplementedPlanServiceHandler
	db           *pgxpool.Pool
	queries      genDb.Querier
	authz        *authz.Authorizer
	resolver     ImageResolver
	registryHost string
	defaults     servicedefaults.Defaults
	secrets      SecretConfig
	silentAfter  time.Duration
}

// NewPlanServer creates a PlanServer; secrets supplies the per-placement size limit the plan
// checks each service's declared secrets against.
func NewPlanServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	resolver ImageResolver,
	registryHost string,
	defaults servicedefaults.Defaults,
	secrets SecretConfig,
	clusterSilentAfter time.Duration,
) *PlanServer {
	return &PlanServer{
		db:           db,
		queries:      queries,
		authz:        authz.New(db, queries),
		resolver:     resolver,
		registryHost: registryHost,
		defaults:     defaults,
		secrets:      secrets,
		silentAfter:  clusterSilentAfter,
	}
}

// plannedFile is a loco.yaml file planned against one environment: the parsed file, the
// environment, the services the environment runs and the plan.
type plannedFile struct {
	file            *locofile.File
	env             genDb.Environment
	live            liveEnvironment
	platformDomains []string
	images          map[string]string
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
	elsewhere   map[uuid.UUID]bool
}

// Plan returns the operations an apply of the file would perform in the environment. It writes
// nothing and needs workspace read.
func (s *PlanServer) Plan(
	ctx context.Context,
	req *planv1.PlanRequest,
) (*planv1.PlanResponse, error) {
	planned, err := s.loadPlan(ctx, req.GetFile(), req.GetEnvironmentId(), actions.Plan, nil)
	if err != nil {
		return nil, err
	}
	return &planv1.PlanResponse{
		Revision:   planned.env.Revision,
		Operations: planOperationsToProto(planned.plan.Operations),
		Errors:     planErrorsToProto(planned.plan.Errors),
		Images:     planned.images,
	}, nil
}

func (s *PlanServer) loadPlan(
	ctx context.Context,
	rawFile []byte,
	rawEnvironmentID string,
	action actions.Action,
	provision []string,
) (*plannedFile, error) {
	file, err := locofile.Parse(rawFile)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidLocofile).WithCause(err)
	}

	environmentID := uuid.MustParse(rawEnvironmentID)
	env, err := visibleEnvironment(ctx, s.queries, s.authz, environmentID, action)
	if err != nil {
		return nil, err
	}

	services, err := locofile.Resolve(file, env.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidLocofile).WithCause(err)
	}

	environments, err := s.queries.ListWorkspaceEnvironments(ctx, env.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	clusters, err := eligibleClusters(ctx, s.queries, env.EnvironmentType, s.silentAfter)
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	live, err := s.liveEnvironment(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("load the environment's services: %w", err)
	}
	secrets, err := s.serviceSecrets(ctx, env.ID)
	if err != nil {
		return nil, fmt.Errorf("list secret names: %w", err)
	}

	environmentNames := make([]string, 0, len(environments))
	for _, environment := range environments {
		environmentNames = append(environmentNames, environment.Name)
	}
	targets := regionClusters(clusters)

	images := s.resolveImages(ctx, services)
	plan, err := configplan.Compute(configplan.Input{
		Partial:          file.Partial,
		Services:         services,
		Disabled:         locofile.Disabled(file, env.Name),
		FileEnvironments: fileEnvironments(file),
		Environments:     environmentNames,
		Clusters:         targets,
		Secrets:          secrets,
		MaxSecretBytes:   int64(s.secrets.MaxPlacementBytes),
		Provision:        provision,
		Live:             live.services,
		Defaults:         s.defaults,
		Images:           images,
	})
	if err != nil {
		return nil, fmt.Errorf("compute plan: %w", err)
	}
	plan.Errors = append(plan.Errors, slugErrors(plan.Operations, live.resources)...)
	if len(plan.Errors) > 0 {
		plan.Operations = nil
		plan.Desired = nil
	}
	return &plannedFile{
		file:            file,
		env:             env,
		live:            live,
		platformDomains: platformdomain.Served(targets),
		images:          pinnedImages(images),
		plan:            plan,
	}, nil
}

// slugErrors reports each service the plan creates whose slug is empty, reserved, or held by
// another workspace resource or another create in the plan.
func slugErrors(operations []configplan.Operation, resources map[string]genDb.Resource) []configplan.Error {
	holders := make(map[string]string, len(resources))
	for name, resource := range resources {
		holders[resource.Slug] = name
	}
	var errs []configplan.Error
	for _, op := range operations {
		if op.Kind != configplan.KindCreate {
			continue
		}
		slug := resourceSlug(op.Service)
		if slug == "" {
			errs = append(errs, configplan.Error{Service: op.Service, Path: "name", Err: errPrivateServiceName})
			continue
		}
		if other, taken := holders[slug]; taken {
			errs = append(errs, configplan.Error{Service: op.Service, Path: "name", Err: slugTakenError(slug, other)})
			continue
		}
		holders[slug] = op.Service
	}
	return errs
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

func pinnedImages(images map[string]configplan.ImageResult) map[string]string {
	pinned := make(map[string]string, len(images))
	for reference, result := range images {
		if result.Err != nil {
			continue
		}
		pinned[reference] = result.Pinned
	}
	return pinned
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
	runningElsewhere, err := s.queries.ListResourcesRunningOutsideEnvironment(
		ctx,
		genDb.ListResourcesRunningOutsideEnvironmentParams{ResourceIds: ids, EnvironmentID: env.ID},
	)
	if err != nil {
		return liveEnvironment{}, fmt.Errorf("list resources running elsewhere: %w", err)
	}

	live := liveEnvironment{
		services:    make([]configplan.Service, 0, len(resources)),
		resources:   make(map[string]genDb.Resource, len(resources)),
		domains:     map[uuid.UUID][]genDb.ResourceDomain{},
		deployments: map[uuid.UUID][]genDb.Deployment{},
		builds:      map[uuid.UUID]genDb.ListLatestSucceededBuildsForResourcesRow{},
		elsewhere:   make(map[uuid.UUID]bool, len(runningElsewhere)),
	}
	for _, resourceID := range runningElsewhere {
		live.elsewhere[resourceID] = true
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
			Name:           res.Name,
			Partial:        derefString(res.Partial),
			Built:          built,
			RunningRegions: deploymentRegions(live.deployments[res.ID]),
			Elsewhere:      live.elsewhere[res.ID],
			State:          state,
		})
	}
	return live, nil
}

// serviceSecrets returns the value size of every secret each service holds in the environment,
// by service name and secret name. The size is the ciphertext length less the GCM tag, so
// nothing is decrypted.
func (s *PlanServer) serviceSecrets(
	ctx context.Context,
	environmentID uuid.UUID,
) (map[string]map[string]int64, error) {
	rows, err := s.queries.ListResourceSecretNames(ctx, environmentID)
	if err != nil {
		return nil, fmt.Errorf("list secret names: %w", err)
	}
	secrets := make(map[string]map[string]int64)
	for _, row := range rows {
		if secrets[row.ResourceName] == nil {
			secrets[row.ResourceName] = make(map[string]int64)
		}
		secrets[row.ResourceName][row.Name] = row.CiphertextBytes - secretTagBytes
	}
	return secrets, nil
}

func deploymentRegions(deployments []genDb.Deployment) []string {
	regions := make([]string, 0, len(deployments))
	for _, deployment := range deployments {
		regions = append(regions, deployment.Region)
	}
	slices.Sort(regions)
	return slices.Compact(regions)
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
		Health:  liveHealth(spec.GetHealthCheck()),
		Regions: make(map[string]configplan.Region, len(spec.GetRegions())),
	}
	if res.MaxApplyAttempts != nil || res.ApplyRetryDelaySeconds != nil {
		state.Deploy = &locofile.Deploy{Attempts: res.MaxApplyAttempts, RetryDelay: res.ApplyRetryDelaySeconds}
	}
	if metrics := spec.GetObservability().GetMetrics(); metrics.GetEnabled() {
		state.Metrics = &locofile.Metrics{Port: metrics.GetPort(), Path: metrics.GetPath()}
		if state.Metrics.Path == "" {
			state.Metrics.Path = s.defaults.MetricsPath
		}
	}
	if routing := spec.GetRouting(); routing.GetPathPrefix() != "" || routing.GetIdleTimeout() != 0 {
		pathPrefix, idleTimeout := s.defaults.Routing(routing.GetPathPrefix(), routing.GetIdleTimeout())
		state.Routing = &configplan.Routing{PathPrefix: pathPrefix, IdleTimeout: idleTimeout}
	}
	slices.SortStableFunc(domains, func(a, b genDb.ResourceDomain) int {
		return boolCompare(b.IsPrimary, a.IsPrimary)
	})
	names := make([]string, 0, len(domains))
	for _, domain := range domains {
		names = append(names, domain.Domain)
	}
	state.Domains = configplan.CanonicalDomains(names)
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
		deploymentSpec, specErr := converter.DeserializeDeploymentSpec(deployment.Spec, res.Type)
		if specErr != nil {
			return configplan.State{}, fmt.Errorf("deployment %s spec: %w", deployment.ID, specErr)
		}
		service := deploymentSpec.GetService()
		target := spec.GetRegions()[deployment.Region]
		if !target.GetEnabled() {
			target = nil
		}
		resources := s.defaults.Fill(target, service)
		state.Regions[deployment.Region] = configplan.Region{
			CPU:         resources.CPU,
			Memory:      resources.Memory,
			MinReplicas: resources.MinReplicas,
			MaxReplicas: resources.MaxReplicas,
			Autoscaling: liveAutoscaling(resources.Scalers),
		}
		if index > 0 {
			continue
		}
		if metrics := service.GetMetrics(); metrics != nil {
			state.Metrics = nil
			if metrics.GetEnabled() {
				state.Metrics = &locofile.Metrics{Port: metrics.GetPort(), Path: metrics.GetPath()}
			}
		}
		state.RunAsUser = service.RunAsUser
		state.RunAsGroup = service.RunAsGroup
		state.Port = servicedefaults.FirstSet(service.GetPort(), state.Port)
		if service.GetRouting() != nil {
			state.Routing = liveRouting(service.GetRouting())
		}
		state.Health = liveHealth(service.GetHealthCheck())
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
		state.Secrets = deployment.SecretNames
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

func liveHealth(health *deploymentv1.HealthCheckConfig) *configplan.Health {
	if health == nil {
		return nil
	}
	return &configplan.Health{
		Path:               health.GetPath(),
		Interval:           health.GetIntervalSeconds(),
		Timeout:            health.GetTimeoutSeconds(),
		FailThreshold:      health.GetFailureThreshold(),
		StartupGracePeriod: health.GetInitialDelaySeconds(),
	}
}

func liveRouting(routing *deploymentv1.ServiceRouting) *configplan.Routing {
	if routing.GetPathPrefix() == "" {
		return nil
	}
	return &configplan.Routing{PathPrefix: routing.GetPathPrefix(), IdleTimeout: routing.GetIdleTimeout()}
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
		panic(fmt.Errorf("%w: %d", errUnknownOperation, kind))
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
