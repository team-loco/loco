package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/platformdomain"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	specVersion = 1
)

var errNoBuildToDeploy = errors.New("the service has no build to deploy")

// applier performs the operations of one plan inside one transaction. builds are the builds the
// request named per service; deployed records the services the operations have deployed;
// provisioning creates services without deploying them.
type applier struct {
	regionTargets   *resourcev1.RegionTargets
	env             genDb.Environment
	organizationID  uuid.UUID
	partial         string
	live            liveEnvironment
	desired         map[string]configplan.State
	builds          map[string]*deploymentv1.BuildSource
	deployed        map[string]bool
	platformDomains []string
	defaults        servicedefaults.Defaults
	started         []*planv1.StartedDeployment
	provisioning    bool
	silentAfter     time.Duration
	commit          *deploymentv1.SourceCommit
	counts          limits.Effective
}

// newApplier returns an applier for one environment with the organization's limits; the caller
// sets what the operations read.
func newApplier(
	env genDb.Environment,
	organizationID uuid.UUID,
	counts limits.Effective,
	defaults servicedefaults.Defaults,
	silentAfter time.Duration,
) *applier {
	return &applier{
		env:            env,
		organizationID: organizationID,
		counts:         counts,
		defaults:       defaults,
		silentAfter:    silentAfter,
		deployed:       map[string]bool{},
	}
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
		panic(fmt.Errorf("%w: %d", errUnknownOperation, op.Kind))
	}
}

func (a *applier) create(ctx context.Context, qtx *genDb.Queries, op configplan.Operation) error {
	principal, err := principalFrom(ctx)
	if err != nil {
		return err
	}
	regions := slices.Sorted(maps.Keys(op.Desired.Regions))
	spec := serviceSpecFor(op.Desired)
	specJSON, err := protojson.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal resource spec: %w", err)
	}
	resourceID, err := qtx.CreateResource(ctx, genDb.CreateResourceParams{
		WorkspaceID: a.env.WorkspaceID,
		CreatedBy:   principal.ActorID,
		Name:        op.Service,
		Type:        genDb.ResourceTypeService,
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
	if deployErr := setDeployPolicy(ctx, qtx, resourceID, op.Desired.Deploy); deployErr != nil {
		return deployErr
	}
	if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); bumpErr != nil {
		return bumpErr
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
	if op.NeedsDeploy || a.provisioning {
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
		if err := retireDeployment(ctx, qtx, res, deployment); err != nil {
			return err
		}
	}
	for _, domain := range a.live.domains[res.ID] {
		if err := qtx.DeleteResourceDomain(ctx, domain.ID); err != nil {
			return fmt.Errorf("delete domain %s: %w", domain.Domain, err)
		}
		if err := events.Record(ctx, qtx, events.Event{
			Type:          events.DomainDeleted,
			EnvironmentID: new(a.env.ID),
			WorkspaceID:   new(res.WorkspaceID),
			ResourceID:    new(res.ID),
			SubjectType:   events.SubjectDomain,
			SubjectID:     new(domain.ID),
			Data:          map[string]any{events.FieldDomain: domain.Domain},
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
	platformDomain, label, err := platformdomain.Match(domain, a.platformDomains)
	if err != nil {
		return uuid.UUID{}, &invalidInputError{public: publicInvalidRequest, err: err}
	}
	domainID, err := qtx.CreateResourceDomain(ctx, genDb.CreateResourceDomainParams{
		ResourceID:     resourceID,
		EnvironmentID:  a.env.ID,
		Domain:         domain,
		DomainSource:   genDb.DomainSourcePlatformProvided,
		SubdomainLabel: &label,
		PlatformDomain: &platformDomain,
		IsPrimary:      false,
	})
	if isDomainTaken(err) {
		return uuid.UUID{}, errDomainInUse
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("create domain %s: %w", domain, err)
	}
	return domainID, events.Record(ctx, qtx, events.Event{
		Type:          events.DomainCreated,
		EnvironmentID: new(a.env.ID),
		WorkspaceID:   new(a.env.WorkspaceID),
		ResourceID:    new(resourceID),
		SubjectType:   events.SubjectDomain,
		SubjectID:     new(domainID),
		Data:          map[string]any{events.FieldDomain: domain, events.FieldResourceID: resourceID.String()},
	})
}

func (a *applier) deploy(
	ctx context.Context,
	qtx *genDb.Queries,
	res genDb.Resource,
	spec *resourcev1.ServiceSpec,
	state configplan.State,
	regions []string,
) error {
	principal, err := principalFrom(ctx)
	if err != nil {
		return err
	}
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
	render := placementRenderer{
		resource:        res,
		organizationID:  a.organizationID,
		resourceSpec:    &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: spec}},
		hostname:        hostname,
		environmentID:   a.env.ID,
		environmentName: a.env.Name,
		defaults:        a.defaults,
	}

	clusters, err := eligibleClusters(ctx, qtx, a.silentAfter, a.defaults.TargetPlatform)
	if err != nil {
		return err
	}
	targets := make([]placementTarget, 0, len(regions))
	for _, region := range regions {
		cluster, found := clusterForRegion(clusters, region)
		if !found {
			return errNoActiveCluster.Detail("%s", region)
		}
		targets = append(targets, placementTarget{region: region, clusterID: cluster.ID})
	}
	service := deploymentSpecFor(state, regions, build)
	service.Platform = string(a.defaults.TargetPlatform)
	if quantityErr := validateQuantities(service); quantityErr != nil {
		return &invalidSpecError{err: quantityErr}
	}
	deploymentID, err := createDeployment(ctx, qtx, &a.counts, newDeployment{
		params: genDb.CreateDeploymentParams{
			ResourceID:    res.ID,
			EnvironmentID: a.env.ID,
			SecretNames:   append([]string{}, state.Secrets...),
			ActorType:     principal.ActorType,
			ActorID:       principal.ActorID,
			Method:        principal.Method,
			CommitSha:     sourceCommitSHA(a.commit),
			CommitDirty:   a.commit.GetDirty(),
			Trigger:       a.trigger(),
		},
		service: service,
		targets: targets,
		render:  render.payload,
	})
	if err != nil {
		return err
	}
	if bumpErr := bumpEnvironmentRevision(ctx, qtx, a.env.ID); bumpErr != nil {
		return bumpErr
	}
	if eventErr := recordDeploymentCreated(ctx, qtx, res, a.env.ID, deploymentID, regions); eventErr != nil {
		return eventErr
	}
	a.started = append(a.started, &planv1.StartedDeployment{
		Service:      res.Name,
		Regions:      regions,
		DeploymentId: deploymentID.String(),
	})
	return nil
}

// trigger is what the deployments of this apply record as their cause.
func (a *applier) trigger() genDb.DeploymentTrigger {
	if a.regionTargets != nil {
		return genDb.DeploymentTriggerScale
	}
	return genDb.DeploymentTriggerDeploy
}

func recordDeploymentCreated(
	ctx context.Context,
	qtx *genDb.Queries,
	res genDb.Resource,
	environmentID, deploymentID uuid.UUID,
	regions []string,
) error {
	return events.Record(ctx, qtx, events.Event{
		Type:          events.DeploymentCreated,
		EnvironmentID: new(environmentID),
		WorkspaceID:   new(res.WorkspaceID),
		ResourceID:    new(res.ID),
		SubjectType:   events.SubjectDeployment,
		SubjectID:     new(deploymentID),
		Data: map[string]any{
			events.FieldResourceID:    res.ID.String(),
			events.FieldEnvironmentID: environmentID.String(),
			events.FieldRegions:       regions,
		},
	})
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
	source := a.builds[res.Name]
	if source == nil {
		if deployments := a.live.deployments[res.ID]; len(deployments) > 0 {
			deploymentSpec, err := converter.DeserializeDeploymentSpec(deployments[0].Spec, res.Type)
			if err != nil {
				return nil, fmt.Errorf("deployment %s spec: %w", deployments[0].ID, err)
			}
			source = deploymentSpec.GetService().GetBuild()
		} else {
			latest, built := a.live.builds[res.ID]
			if !built {
				return nil, fmt.Errorf("%w: %s", errNoBuildToDeploy, res.Name)
			}
			source = &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: new(latest.ID.String())}
		}
	}
	return pinDockerfileBuild(ctx, qtx, res.ID, source, a.defaults.TargetPlatform)
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
	stored := proto.CloneOf(service)
	stored.Env = nil
	specJSON, err := protojson.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("marshal deployment spec: %w", err)
	}
	return specJSON, nil
}

func serviceSpecFor(state configplan.State) *resourcev1.ServiceSpec {
	routing := &resourcev1.RoutingConfig{Port: state.Port}
	if state.Routing != nil {
		routing.PathPrefix = state.Routing.PathPrefix
		routing.IdleTimeout = state.Routing.IdleTimeout
	}
	regions := make(map[string]*resourcev1.RegionTarget, len(state.Regions))
	for name, region := range state.Regions {
		regions[name] = &resourcev1.RegionTarget{
			Enabled:     true,
			Cpu:         region.CPU,
			Memory:      region.Memory,
			MinReplicas: region.MinReplicas,
			MaxReplicas: region.MaxReplicas,
			Scalers:     scalersFor(region.Autoscaling),
		}
	}
	return &resourcev1.ServiceSpec{
		RollbackOnFailure: state.RollbackOnFailure,
		Observability:     state.Observability.Proto(),
		Routing:           routing,
		HealthCheck:       healthCheckFor(state.Health),
		Regions:           regions,
	}
}

func deploymentSpecFor(
	state configplan.State,
	regions []string,
	build *deploymentv1.BuildSource,
) *deploymentv1.ServiceDeploymentSpec {
	targets := make(map[string]*deploymentv1.RegionDeploymentTarget, len(regions))
	for _, region := range regions {
		target := state.Regions[region]
		targets[region] = &deploymentv1.RegionDeploymentTarget{
			Cpu:         &target.CPU,
			Memory:      &target.Memory,
			MinReplicas: &target.MinReplicas,
			MaxReplicas: &target.MaxReplicas,
			Scalers:     scalersFor(target.Autoscaling),
		}
	}
	return &deploymentv1.ServiceDeploymentSpec{
		Observability: state.Observability.Proto(),
		RunAsUser:     state.RunAsUser,
		RunAsGroup:    state.RunAsGroup,
		Build:         build,
		Command:       state.Command,
		Args:          state.Args,
		HealthCheck:   healthCheckFor(state.Health),
		Regions:       targets,
		Env:           state.Env,
		Port:          state.Port,
		Routing:       deploymentRoutingFor(state.Routing),
	}
}

func deploymentRoutingFor(routing *configplan.Routing) *deploymentv1.ServiceRouting {
	if routing == nil {
		return &deploymentv1.ServiceRouting{}
	}
	return &deploymentv1.ServiceRouting{PathPrefix: routing.PathPrefix, IdleTimeout: routing.IdleTimeout}
}

func healthCheckFor(health *configplan.Health) *deploymentv1.HealthCheckConfig {
	if health == nil {
		return nil
	}
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

func setDeployPolicy(ctx context.Context, qtx *genDb.Queries, resourceID uuid.UUID, deploy *locofile.Deploy) error {
	params := genDb.SetResourceDeployPolicyParams{ID: resourceID}
	if deploy != nil {
		params.MaxApplyAttempts = deploy.Attempts
		params.ApplyRetryDelaySeconds = deploy.RetryDelay
	}
	if err := qtx.SetResourceDeployPolicy(ctx, params); err != nil {
		return fmt.Errorf("set resource deploy policy: %w", err)
	}
	return nil
}
