package service

import (
	"context"
	"fmt"
	"slices"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/platformdomain"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	errPartialRegionEdit     = rpcerr.NewCoded(connect.CodeFailedPrecondition, "managed by loco.yaml; edit its partial")
	errResourceRevisionMoved = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"the resource changed since it was loaded",
	)
	errRegionTemplateMissing = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"deploy this resource before updating its regions",
	)
)

func (s *ResourceServer) updateRegions(
	ctx context.Context,
	qtx *genDb.Queries,
	res genDb.Resource,
	req *resourcev1.UpdateResourceRequest,
) error {
	if res.Partial != nil {
		return errPartialRegionEdit
	}
	if res.Revision != req.GetRevision() {
		return errResourceRevisionMoved
	}
	if res.Type != genDb.ResourceTypeService {
		return errOnlyServiceResources
	}
	deployments, err := qtx.ListLatestResourceDeployments(ctx, res.ID)
	if err != nil {
		return fmt.Errorf("list resource deployments: %w", err)
	}
	if len(deployments) == 0 {
		return errRegionTemplateMissing
	}
	domains, err := qtx.ListResourceDomains(ctx, res.ID)
	if err != nil {
		return fmt.Errorf("list resource domains: %w", err)
	}
	organizationID, err := qtx.GetOrganizationIDByWorkspaceID(ctx, res.WorkspaceID)
	if err != nil {
		return fmt.Errorf("get workspace organization: %w", err)
	}
	counts, err := limits.ForOrg(ctx, qtx, s.counts, organizationID)
	if err != nil {
		return err
	}
	environments, err := qtx.ListWorkspaceEnvironments(ctx, res.WorkspaceID)
	if err != nil {
		return fmt.Errorf("list environments: %w", err)
	}
	for _, env := range environments {
		var history, active []genDb.Deployment
		for _, deployment := range deployments {
			if deployment.EnvironmentID != env.ID {
				continue
			}
			history = append(history, deployment)
			if deployment.IsActive {
				active = append(active, deployment)
			}
		}
		if len(history) == 0 {
			continue
		}
		// liveState reads the image, env and commit from its first deployment, which must be
		// what runs now; a retired region's last deployment can carry an older build.
		runtime := active
		if len(runtime) == 0 {
			runtime = []genDb.Deployment{slices.MaxFunc(history, func(a, b genDb.Deployment) int {
				return a.CreatedAt.Compare(b.CreatedAt)
			})}
		}
		var envDomains []genDb.ResourceDomain
		for _, domain := range domains {
			if domain.EnvironmentID == env.ID {
				envDomains = append(envDomains, domain)
			}
		}
		state, stateErr := liveState(ctx, qtx, s.defaults, res, envDomains, runtime)
		if stateErr != nil {
			return stateErr
		}
		current := configplan.Service{
			Name:           res.Name,
			Built:          true,
			RunningRegions: deploymentRegions(active),
			State:          state,
		}
		if len(active) == 0 {
			current.State.Regions = map[string]configplan.Region{}
		}
		desired := state
		desired.Regions = make(map[string]configplan.Region, len(req.GetRegions().GetTargets()))
		for name, target := range req.GetRegions().GetTargets() {
			if !target.GetEnabled() {
				continue
			}
			desired.Regions[name] = configplan.Region{
				CPU:         target.GetCpu(),
				Memory:      target.GetMemory(),
				MinReplicas: target.GetMinReplicas(),
				MaxReplicas: target.GetMaxReplicas(),
				Autoscaling: liveAutoscaling(target.GetScalers()),
			}
		}
		clusters, clusterErr := eligibleClusters(ctx, qtx, env.EnvironmentType, s.clusterSilentAfter)
		if clusterErr != nil {
			return clusterErr
		}
		targets := regionClusters(clusters)
		if errs := configplan.ValidateState(res.Name, desired, targets); len(errs) > 0 {
			return applyRefused(errPlanHasErrors, &planv1.ApplyRefusal{Errors: planErrorsToProto(errs)})
		}
		builds := map[string]*deploymentv1.BuildSource{}
		if len(active) == 0 && desired.Image == "" {
			template, templateErr := currentServiceSpec(runtime[0], res.Type)
			if templateErr != nil {
				return templateErr
			}
			builds[res.Name] = template.GetBuild()
		}
		var commit *deploymentv1.SourceCommit
		if sha := runtime[0].CommitSha; sha != nil {
			commit = &deploymentv1.SourceCommit{Sha: *sha, Dirty: runtime[0].CommitDirty}
		}
		a := applier{
			env:            env,
			organizationID: organizationID,
			counts:         counts,
			live: liveEnvironment{
				resources:   map[string]genDb.Resource{res.Name: res},
				deployments: map[uuid.UUID][]genDb.Deployment{res.ID: active},
				domains:     map[uuid.UUID][]genDb.ResourceDomain{res.ID: envDomains},
			},
			defaults:        s.defaults,
			silentAfter:     s.clusterSilentAfter,
			builds:          builds,
			deployed:        map[string]bool{},
			platformDomains: platformdomain.Served(targets),
			commit:          commit,
			regionTargets:   req.GetRegions(),
		}
		if applyErr := a.apply(ctx, qtx, configplan.Update(current, desired)); applyErr != nil {
			return applyErr
		}
	}
	return nil
}
