package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	applyDeploymentMessage = "Applied loco.yaml"
	specVersion            = 1
)

var (
	errUnknownOperation = errors.New("unknown plan operation")
	errNoBuildToDeploy  = errors.New("the service has no build to deploy")
)

// applier performs the operations of one plan inside one transaction. builds are the builds the
// request named per service; deployed records the services the operations have deployed.
type applier struct {
	env             genDb.Environment
	partial         string
	live            liveEnvironment
	desired         map[string]configplan.State
	builds          map[string]*deploymentv1.BuildSource
	deployed        map[string]bool
	platformDomains []genDb.PlatformDomain
	defaults        servicedefaults.Defaults
	started         []*planv1.StartedDeployment
}

func (a *applier) apply(ctx context.Context, qtx *genDb.Queries, op configplan.Operation) error {
	switch op.Kind {
	case configplan.KindCreate:
		return a.create(ctx, qtx, op)
	case configplan.KindImport:
		return a.importService(ctx, qtx, op)
	case configplan.KindUpdate:
		return a.update(ctx, qtx, op)
	case configplan.KindDelete:
		return a.delete(ctx, qtx, op)
	default:
		return fmt.Errorf("%w: %d", errUnknownOperation, op.Kind)
	}
}

func (a *applier) create(ctx context.Context, qtx *genDb.Queries, op configplan.Operation) error {
	regions := slices.Sorted(maps.Keys(op.Desired.Regions))
	spec := serviceSpecFor(op.Desired, regions[0])
	specJSON, err := protojson.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal resource spec: %w", err)
	}
	resourceID, err := qtx.CreateResource(ctx, genDb.CreateResourceParams{
		WorkspaceID: a.env.WorkspaceID,
		Name:        op.Service,
		Type:        genDb.ResourceTypeService,
		Status:      genDb.ResourceStatusUnavailable,
		Spec:        specJSON,
		SpecVersion: specVersion,
	})
	if err != nil {
		return fmt.Errorf("create resource: %w", err)
	}
	partialParams := genDb.SetResourcePartialParams{ID: resourceID, Partial: &a.partial}
	if partialErr := qtx.SetResourcePartial(ctx, partialParams); partialErr != nil {
		return fmt.Errorf("set resource partial: %w", partialErr)
	}
	if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); bumpErr != nil {
		return bumpErr
	}
	for index, region := range regions {
		_, regionErr := qtx.CreateResourceRegion(ctx, genDb.CreateResourceRegionParams{
			ResourceID: resourceID,
			Region:     region,
			IsPrimary:  index == 0,
			Status:     genDb.RegionIntentStatusDesired,
		})
		if regionErr != nil {
			return fmt.Errorf("create resource region %s: %w", region, regionErr)
		}
	}
	if domainErr := a.syncDomains(ctx, qtx, resourceID, op.Desired.Domains); domainErr != nil {
		return domainErr
	}
	if eventErr := events.Record(ctx, qtx, events.Event{
		Type:        events.ResourceCreated,
		WorkspaceID: new(a.env.WorkspaceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(resourceID),
		Data:        map[string]any{events.FieldName: op.Service, events.FieldPartial: a.partial},
	}); eventErr != nil {
		return eventErr
	}
	if op.NeedsDeploy {
		return nil
	}
	res, err := qtx.GetResourceByID(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("get resource: %w", err)
	}
	return a.deploy(ctx, qtx, res, spec, op.Desired, regions)
}

func (a *applier) importService(ctx context.Context, qtx *genDb.Queries, op configplan.Operation) error {
	res := a.live.resources[op.Service]
	if err := qtx.SetResourcePartial(ctx, genDb.SetResourcePartialParams{ID: res.ID, Partial: &a.partial}); err != nil {
		return fmt.Errorf("set resource partial: %w", err)
	}
	if err := bumpResourceEnvironmentRevisions(ctx, qtx, res.ID); err != nil {
		return err
	}
	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.ResourcePartial,
		WorkspaceID: new(res.WorkspaceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(res.ID),
		Data:        map[string]any{events.FieldPartial: a.partial, events.FieldFrom: ""},
	}); err != nil {
		return err
	}
	return a.update(ctx, qtx, op)
}

func (a *applier) delete(ctx context.Context, qtx *genDb.Queries, op configplan.Operation) error {
	res := a.live.resources[op.Service]
	if a.live.elsewhere[res.ID] {
		return a.removeFromEnvironment(ctx, qtx, res)
	}
	if err := removeResourcePlacements(ctx, qtx, res.ID); err != nil {
		return err
	}
	if err := bumpResourceEnvironmentRevisions(ctx, qtx, res.ID); err != nil {
		return err
	}
	if err := qtx.DeleteResource(ctx, res.ID); err != nil {
		return fmt.Errorf("delete resource: %w", err)
	}
	return events.Record(ctx, qtx, events.Event{
		Type:        events.ResourceDeleted,
		WorkspaceID: new(res.WorkspaceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(res.ID),
		Data:        map[string]any{events.FieldName: res.Name, events.FieldPartial: a.partial},
	})
}

// removeFromEnvironment deletes what the environment holds of a service another environment
// still runs: its deployments and placements and its domains. The resource stays.
func (a *applier) removeFromEnvironment(ctx context.Context, qtx *genDb.Queries, res genDb.Resource) error {
	for _, deployment := range a.live.deployments[res.ID] {
		if err := removePlacement(ctx, qtx, res.ID, deployment.ClusterID); err != nil {
			return err
		}
		finalErr := qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
			ID:       deployment.ID,
			Status:   finalizedDeploymentStatus(deployment.Status),
			IsActive: false,
		})
		if finalErr != nil {
			return fmt.Errorf("finalize deployment %s: %w", deployment.ID, finalErr)
		}
		if err := events.Record(ctx, qtx, events.Event{
			Type:        events.DeploymentDeleted,
			WorkspaceID: new(res.WorkspaceID),
			SubjectType: events.SubjectDeployment,
			SubjectID:   new(deployment.ID),
			Data:        map[string]any{events.FieldResourceID: res.ID.String()},
		}); err != nil {
			return err
		}
	}
	for _, domain := range a.live.domains[res.ID] {
		if err := qtx.DeleteResourceDomain(ctx, domain.ID); err != nil {
			return fmt.Errorf("delete domain %s: %w", domain.Domain, err)
		}
		if err := events.Record(ctx, qtx, events.Event{
			Type:        events.DomainDeleted,
			WorkspaceID: new(res.WorkspaceID),
			SubjectType: events.SubjectDomain,
			SubjectID:   new(domain.ID),
			Data:        map[string]any{events.FieldDomain: domain.Domain},
		}); err != nil {
			return err
		}
	}
	return bumpEnvironmentRevision(ctx, qtx, a.env.ID)
}

func (a *applier) createDomain(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID uuid.UUID,
	domain string,
) (uuid.UUID, error) {
	platformDomain, label, err := a.platformDomainFor(domain)
	if err != nil {
		return uuid.UUID{}, err
	}
	domainID, err := qtx.CreateResourceDomain(ctx, genDb.CreateResourceDomainParams{
		ResourceID:       resourceID,
		EnvironmentID:    a.env.ID,
		Domain:           domain,
		DomainSource:     genDb.DomainSourcePlatformProvided,
		SubdomainLabel:   &label,
		PlatformDomainID: &platformDomain.ID,
		IsPrimary:        false,
	})
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("create domain %s: %w", domain, err)
	}
	return domainID, events.Record(ctx, qtx, events.Event{
		Type:        events.DomainCreated,
		WorkspaceID: new(a.env.WorkspaceID),
		SubjectType: events.SubjectDomain,
		SubjectID:   new(domainID),
		Data:        map[string]any{events.FieldDomain: domain, events.FieldResourceID: resourceID.String()},
	})
}

func (a *applier) platformDomainFor(domain string) (genDb.PlatformDomain, string, error) {
	names := make([]string, 0, len(a.platformDomains))
	for _, platformDomain := range a.platformDomains {
		names = append(names, platformDomain.Domain)
	}
	matched, label, err := configplan.MatchPlatformDomain(domain, names)
	if err != nil {
		return genDb.PlatformDomain{}, "", connect.NewError(connect.CodeInvalidArgument, err)
	}
	index := slices.Index(names, matched)
	return a.platformDomains[index], label, nil
}

func (a *applier) deploy(
	ctx context.Context,
	qtx *genDb.Queries,
	res genDb.Resource,
	spec *resourcev1.ServiceSpec,
	state configplan.State,
	regions []string,
) error {
	build, err := a.currentBuild(ctx, qtx, res, state)
	if err != nil {
		return err
	}
	if lockErr := lockPinnedBuildImage(ctx, qtx, build); lockErr != nil {
		return lockErr
	}
	a.deployed[res.Name] = true
	hostname, err := primaryHostname(ctx, qtx, res.ID, a.env.ID)
	if err != nil {
		return err
	}
	resourceSpec := &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: spec}}
	secretNames := append([]string{}, state.Secrets...)

	clusters, err := eligibleClusters(ctx, qtx, a.env.EnvironmentType)
	if err != nil {
		return err
	}
	for _, region := range regions {
		cluster, found := clusterForRegion(clusters, region)
		if !found {
			return fmt.Errorf("%w: %s (%s)", errNoActiveCluster, region, a.env.EnvironmentType)
		}
		service := deploymentSpecFor(state, region, build)
		if quantityErr := validateQuantities(service); quantityErr != nil {
			return &invalidSpecError{err: quantityErr}
		}
		specJSON, marshalErr := deploymentSpecJSON(service)
		if marshalErr != nil {
			return marshalErr
		}
		deploymentSpec := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}}
		buildSpec := desiredApplicationSpec(
			res,
			resourceSpec,
			hostname,
			deploymentSpec,
			region,
			a.env.ID,
			a.env.Name,
			a.defaults,
		)
		deploymentID, deployErr := createDeploymentWithCleanup(ctx, qtx, genDb.CreateDeploymentParams{
			ResourceID:    res.ID,
			ClusterID:     cluster.ID,
			Region:        region,
			Replicas:      service.GetMinReplicas(),
			Status:        genDb.DeploymentStatusPending,
			IsActive:      true,
			Message:       applyDeploymentMessage,
			Spec:          specJSON,
			SpecVersion:   specVersion,
			EnvironmentID: a.env.ID,
			SecretNames:   secretNames,
		}, buildSpec)
		if deployErr != nil {
			return deployErr
		}
		if bumpErr := bumpEnvironmentRevision(ctx, qtx, a.env.ID); bumpErr != nil {
			return bumpErr
		}
		if eventErr := events.Record(ctx, qtx, events.Event{
			Type:        events.DeploymentCreated,
			WorkspaceID: new(res.WorkspaceID),
			SubjectType: events.SubjectDeployment,
			SubjectID:   new(deploymentID),
			Data: map[string]any{
				events.FieldResourceID: res.ID.String(),
				"environmentId":        a.env.ID.String(),
			},
		}); eventErr != nil {
			return eventErr
		}
		a.started = append(a.started, &planv1.StartedDeployment{
			Service:      res.Name,
			Region:       region,
			DeploymentId: deploymentID.String(),
		})
	}
	return nil
}

func (a *applier) currentBuild(
	ctx context.Context,
	qtx *genDb.Queries,
	res genDb.Resource,
	state configplan.State,
) (*deploymentv1.BuildSource, error) {
	if state.Image != "" {
		return &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: state.Image}, nil
	}
	if requested := a.builds[res.Name]; requested != nil {
		return requested, nil
	}
	if deployments := a.live.deployments[res.ID]; len(deployments) > 0 {
		deploymentSpec, err := converter.DeserializeDeploymentSpec(deployments[0].Spec, string(res.Type))
		if err != nil {
			return nil, fmt.Errorf("deployment %s spec: %w", deployments[0].ID, err)
		}
		return deploymentSpec.GetService().GetBuild(), nil
	}
	latest, built := a.live.builds[res.ID]
	if !built {
		return nil, fmt.Errorf("%w: %s", errNoBuildToDeploy, res.Name)
	}
	build, err := qtx.GetBuildByID(ctx, latest.ID)
	if err != nil {
		return nil, fmt.Errorf("get build %s: %w", latest.ID, err)
	}
	image := build.ImageRepository + "@" + derefString(build.ImageDigest)
	buildID := build.ID.String()
	return &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, Image: image, BuildId: &buildID}, nil
}

func (a *applier) deployBuilds(ctx context.Context, qtx *genDb.Queries) error {
	for _, service := range slices.Sorted(maps.Keys(a.builds)) {
		if a.deployed[service] {
			continue
		}
		res := a.live.resources[service]
		resourceSpec, err := converter.DeserializeResourceSpec(res.Spec, res.Type)
		if err != nil {
			return fmt.Errorf("resource spec: %w", err)
		}
		desired := a.desired[service]
		regions := slices.Sorted(maps.Keys(desired.Regions))
		if deployErr := a.deploy(ctx, qtx, res, resourceSpec.GetService(), desired, regions); deployErr != nil {
			return deployErr
		}
	}
	return nil
}

func deploymentSpecJSON(service *deploymentv1.ServiceDeploymentSpec) ([]byte, error) {
	cloned := proto.Clone(service)
	stored, ok := cloned.(*deploymentv1.ServiceDeploymentSpec)
	if !ok {
		return nil, errCloneServiceSpec
	}
	stored.Env = nil
	specJSON, err := protojson.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("marshal deployment spec: %w", err)
	}
	return specJSON, nil
}

func serviceSpecFor(state configplan.State, primary string) *resourcev1.ServiceSpec {
	routing := &resourcev1.RoutingConfig{Port: state.Port}
	if state.Routing != nil {
		routing.PathPrefix = state.Routing.PathPrefix
		routing.IdleTimeout = state.Routing.IdleTimeout
	}
	regions := make(map[string]*resourcev1.RegionTarget, len(state.Regions))
	for name, region := range state.Regions {
		regions[name] = &resourcev1.RegionTarget{
			Enabled:     true,
			Primary:     name == primary,
			Cpu:         region.CPU,
			Memory:      region.Memory,
			MinReplicas: region.MinReplicas,
			MaxReplicas: region.MaxReplicas,
			Scalers:     scalersFor(region.Autoscaling),
		}
	}
	return &resourcev1.ServiceSpec{
		Routing:     routing,
		HealthCheck: healthCheckFor(state.Health),
		Regions:     regions,
	}
}

func deploymentSpecFor(
	state configplan.State,
	region string,
	build *deploymentv1.BuildSource,
) *deploymentv1.ServiceDeploymentSpec {
	target := state.Regions[region]
	return &deploymentv1.ServiceDeploymentSpec{
		Build:       build,
		HealthCheck: healthCheckFor(state.Health),
		Cpu:         &target.CPU,
		Memory:      &target.Memory,
		MinReplicas: &target.MinReplicas,
		MaxReplicas: &target.MaxReplicas,
		Scalers:     scalersFor(target.Autoscaling),
		Env:         state.Env,
		Port:        state.Port,
		Routing:     deploymentRoutingFor(state.Routing),
	}
}

func deploymentRoutingFor(routing *configplan.Routing) *deploymentv1.ServiceRouting {
	if routing == nil {
		return &deploymentv1.ServiceRouting{}
	}
	return &deploymentv1.ServiceRouting{PathPrefix: routing.PathPrefix, IdleTimeout: routing.IdleTimeout}
}

func healthCheckFor(health configplan.Health) *deploymentv1.HealthCheckConfig {
	return &deploymentv1.HealthCheckConfig{
		Path:                health.Path,
		InitialDelaySeconds: health.StartupGracePeriod,
		IntervalSeconds:     health.Interval,
		TimeoutSeconds:      health.Timeout,
		FailureThreshold:    health.FailThreshold,
	}
}

func scalersFor(autoscaling *configplan.Autoscaling) *deploymentv1.Scalers {
	if autoscaling == nil {
		return nil
	}
	return &deploymentv1.Scalers{
		Enabled:      true,
		CpuTarget:    &autoscaling.CPUTarget,
		MemoryTarget: &autoscaling.MemoryTarget,
	}
}
