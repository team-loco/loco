package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

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
	errUpdateNotApplied = errors.New("updates cannot be applied yet")
	errNoActiveCluster  = errors.New("no healthy cluster for the region and environment type")
	errNoBuildToDeploy  = errors.New("the service has no build to deploy")
)

// applier performs the operations of one plan inside one transaction.
type applier struct {
	env             genDb.Environment
	partial         string
	live            liveEnvironment
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
		return connect.NewError(connect.CodeUnimplemented, errUpdateNotApplied)
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
	if domainErr := a.addDomains(ctx, qtx, resourceID, op.Desired.Domains, false); domainErr != nil {
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
	if _, err := qtx.LockResource(ctx, res.ID); err != nil {
		return fmt.Errorf("lock resource: %w", err)
	}
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
	if len(op.Changes) > 0 {
		return connect.NewError(connect.CodeUnimplemented, errUpdateNotApplied)
	}
	if op.NeedsDeploy || len(a.live.deployments[res.ID]) > 0 {
		return nil
	}
	resourceSpec, err := converter.DeserializeResourceSpec(res.Spec, res.Type)
	if err != nil {
		return fmt.Errorf("resource spec: %w", err)
	}
	regions := slices.Sorted(maps.Keys(op.Desired.Regions))
	return a.deploy(ctx, qtx, res, resourceSpec.GetService(), op.Desired, regions)
}

func (a *applier) delete(ctx context.Context, qtx *genDb.Queries, op configplan.Operation) error {
	res := a.live.resources[op.Service]
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

func (a *applier) addDomains(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID uuid.UUID,
	domains []string,
	hasPrimary bool,
) error {
	for index, domain := range domains {
		platformDomain, label, err := a.platformDomainFor(domain)
		if err != nil {
			return err
		}
		domainID, err := qtx.CreateResourceDomain(ctx, genDb.CreateResourceDomainParams{
			ResourceID:       resourceID,
			Domain:           domain,
			DomainSource:     genDb.DomainSourcePlatformProvided,
			SubdomainLabel:   &label,
			PlatformDomainID: &platformDomain.ID,
			IsPrimary:        index == 0 && !hasPrimary,
		})
		if err != nil {
			return fmt.Errorf("create domain %s: %w", domain, err)
		}
		if err := events.Record(ctx, qtx, events.Event{
			Type:        events.DomainCreated,
			WorkspaceID: new(a.env.WorkspaceID),
			SubjectType: events.SubjectDomain,
			SubjectID:   new(domainID),
			Data:        map[string]any{events.FieldDomain: domain, events.FieldResourceID: resourceID.String()},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a *applier) platformDomainFor(domain string) (genDb.PlatformDomain, string, error) {
	for _, platformDomain := range a.platformDomains {
		label, found := strings.CutSuffix(domain, "."+platformDomain.Domain)
		if !found {
			continue
		}
		if label == "" || strings.Contains(label, ".") {
			invalid := fmt.Errorf("domain %s must be a single label under %s", domain, platformDomain.Domain)
			return genDb.PlatformDomain{}, "", connect.NewError(connect.CodeInvalidArgument, invalid)
		}
		return platformDomain, label, nil
	}
	return genDb.PlatformDomain{}, "", fmt.Errorf("%w: %s", configplan.ErrCustomDomain, domain)
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
	hostname, err := primaryHostname(ctx, qtx, res.ID)
	if err != nil {
		return err
	}
	resourceSpec := &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: spec}}
	secretNames := append([]string{}, state.Secrets...)

	for _, region := range regions {
		cluster, clusterErr := qtx.GetActiveClusterByRegionAndTier(ctx, genDb.GetActiveClusterByRegionAndTierParams{
			Region: region,
			Tier:   a.env.EnvironmentType,
		})
		if clusterErr != nil {
			return fmt.Errorf("%w: %s: %w", errNoActiveCluster, region, clusterErr)
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
	}
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
