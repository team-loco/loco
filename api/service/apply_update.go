package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/converter"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	regionsPathPrefix = "regions."
	pathEnabled       = "enabled"
)

func (a *applier) update(ctx context.Context, qtx *genDb.Queries, op configplan.Operation) error {
	res := a.live.resources[op.Service]
	if stopsService(op) {
		return a.stop(ctx, qtx, res, op)
	}
	currentSpec, err := converter.DeserializeResourceSpec(res.Spec, res.Type)
	if err != nil {
		return fmt.Errorf("resource spec: %w", err)
	}
	current := currentSpec.GetService()
	regions := slices.Sorted(maps.Keys(op.Desired.Regions))
	spec := serviceSpecFor(op.Desired, primaryRegion(current, regions))
	spec.Observability = current.GetObservability()
	for name, target := range current.GetRegions() {
		if _, kept := spec.GetRegions()[name]; kept {
			continue
		}
		disabled := &resourcev1.RegionTarget{Cpu: target.GetCpu(), Memory: target.GetMemory()}
		spec.Regions[name] = disabled
	}
	specJSON, err := protojson.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal resource spec: %w", err)
	}
	specParams := genDb.UpdateResourceSpecParams{ID: res.ID, Spec: specJSON}
	if updateErr := qtx.UpdateResourceSpec(ctx, specParams); updateErr != nil {
		return fmt.Errorf("update resource spec: %w", updateErr)
	}
	if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, res.ID); bumpErr != nil {
		return bumpErr
	}
	if regionErr := ensureRegions(ctx, qtx, res.ID, regions); regionErr != nil {
		return regionErr
	}
	if domainErr := a.syncDomains(ctx, qtx, res.ID, op.Desired.Domains); domainErr != nil {
		return domainErr
	}
	if eventErr := events.Record(ctx, qtx, events.Event{
		Type:        events.ResourceUpdated,
		WorkspaceID: new(res.WorkspaceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(res.ID),
		Data:        map[string]any{events.FieldPartial: a.partial, events.FieldPaths: changePaths(op)},
	}); eventErr != nil {
		return eventErr
	}
	if stopErr := a.stopRemovedRegions(ctx, qtx, res, op.Desired.Regions); stopErr != nil {
		return stopErr
	}
	if op.NeedsDeploy {
		return nil
	}
	affected := a.affectedRegions(res.ID, op.Changes, regions)
	if len(affected) == 0 {
		return nil
	}
	return a.deploy(ctx, qtx, res, spec, op.Desired, affected)
}

func stopsService(op configplan.Operation) bool {
	for _, change := range op.Changes {
		if change.Path == pathEnabled && change.After == "false" {
			return true
		}
	}
	return false
}

func (a *applier) stop(ctx context.Context, qtx *genDb.Queries, res genDb.Resource, op configplan.Operation) error {
	if stopErr := a.stopRemovedRegions(ctx, qtx, res, nil); stopErr != nil {
		return stopErr
	}
	return events.Record(ctx, qtx, events.Event{
		Type:        events.ResourceUpdated,
		WorkspaceID: new(res.WorkspaceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(res.ID),
		Data:        map[string]any{events.FieldPartial: a.partial, events.FieldPaths: changePaths(op)},
	})
}

func changePaths(op configplan.Operation) []string {
	paths := make([]string, 0, len(op.Changes))
	for _, change := range op.Changes {
		paths = append(paths, change.Path)
	}
	return paths
}

func primaryRegion(current *resourcev1.ServiceSpec, regions []string) string {
	for name, target := range current.GetRegions() {
		if target.GetPrimary() && slices.Contains(regions, name) {
			return name
		}
	}
	return regions[0]
}

func ensureRegions(ctx context.Context, qtx *genDb.Queries, resourceID uuid.UUID, regions []string) error {
	rows, err := qtx.ListResourceRegions(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("list resource regions: %w", err)
	}
	existing := make(map[string]bool, len(rows))
	for _, row := range rows {
		existing[row.Region] = true
	}
	for _, region := range regions {
		if existing[region] {
			continue
		}
		_, createErr := qtx.CreateResourceRegion(ctx, genDb.CreateResourceRegionParams{
			ResourceID: resourceID,
			Region:     region,
			IsPrimary:  false,
			Status:     genDb.RegionIntentStatusDesired,
		})
		if createErr != nil {
			return fmt.Errorf("create resource region %s: %w", region, createErr)
		}
	}
	return nil
}

func (a *applier) stopRemovedRegions(
	ctx context.Context,
	qtx *genDb.Queries,
	res genDb.Resource,
	desired map[string]configplan.Region,
) error {
	for _, deployment := range a.live.deployments[res.ID] {
		if _, kept := desired[deployment.Region]; kept {
			continue
		}
		if removeErr := removePlacement(ctx, qtx, res.ID, deployment.ClusterID); removeErr != nil {
			return removeErr
		}
		finalErr := qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
			ID:       deployment.ID,
			Status:   finalizedDeploymentStatus(deployment.Status),
			IsActive: false,
		})
		if finalErr != nil {
			return fmt.Errorf("finalize deployment %s: %w", deployment.ID, finalErr)
		}
		if bumpErr := bumpEnvironmentRevision(ctx, qtx, a.env.ID); bumpErr != nil {
			return bumpErr
		}
		if eventErr := events.Record(ctx, qtx, events.Event{
			Type:        events.DeploymentDeleted,
			WorkspaceID: new(res.WorkspaceID),
			SubjectType: events.SubjectDeployment,
			SubjectID:   new(deployment.ID),
			Data:        map[string]any{events.FieldResourceID: res.ID.String()},
		}); eventErr != nil {
			return eventErr
		}
	}
	return nil
}

func (a *applier) affectedRegions(resourceID uuid.UUID, changes []configplan.Change, regions []string) []string {
	active := map[string]bool{}
	for _, deployment := range a.live.deployments[resourceID] {
		active[deployment.Region] = true
	}
	global := false
	changed := map[string]bool{}
	for _, change := range changes {
		rest, isRegion := strings.CutPrefix(change.Path, regionsPathPrefix)
		if !isRegion {
			global = true
			continue
		}
		region, _, _ := strings.Cut(rest, ".")
		changed[region] = true
	}
	var affected []string
	for _, region := range regions {
		if global || changed[region] || !active[region] {
			affected = append(affected, region)
		}
	}
	return affected
}

func (a *applier) syncDomains(ctx context.Context, qtx *genDb.Queries, resourceID uuid.UUID, desired []string) error {
	ids := make(map[string]uuid.UUID, len(desired))
	primary := ""
	for _, row := range a.live.domains[resourceID] {
		if slices.Contains(desired, row.Domain) {
			ids[row.Domain] = row.ID
			if row.IsPrimary {
				primary = row.Domain
			}
			continue
		}
		if deleteErr := qtx.DeleteResourceDomain(ctx, row.ID); deleteErr != nil {
			return fmt.Errorf("delete domain %s: %w", row.Domain, deleteErr)
		}
		if eventErr := events.Record(ctx, qtx, events.Event{
			Type:        events.DomainDeleted,
			WorkspaceID: new(a.env.WorkspaceID),
			SubjectType: events.SubjectDomain,
			SubjectID:   new(row.ID),
			Data:        map[string]any{events.FieldDomain: row.Domain},
		}); eventErr != nil {
			return eventErr
		}
	}
	for _, domain := range desired {
		if _, exists := ids[domain]; exists {
			continue
		}
		id, createErr := a.createDomain(ctx, qtx, resourceID, domain)
		if createErr != nil {
			return createErr
		}
		ids[domain] = id
	}
	if len(desired) == 0 || primary == desired[0] {
		return nil
	}
	clearParams := genDb.UpdateResourceDomainPrimaryParams{ResourceID: resourceID, EnvironmentID: a.env.ID}
	if clearErr := qtx.UpdateResourceDomainPrimary(ctx, clearParams); clearErr != nil {
		return fmt.Errorf("clear primary domain: %w", clearErr)
	}
	primaryID := ids[desired[0]]
	setParams := genDb.SetResourceDomainPrimaryParams{ID: primaryID, ResourceID: resourceID, EnvironmentID: a.env.ID}
	if _, setErr := qtx.SetResourceDomainPrimary(ctx, setParams); setErr != nil {
		return fmt.Errorf("set primary domain: %w", setErr)
	}
	return events.Record(ctx, qtx, events.Event{
		Type:        events.DomainUpdated,
		WorkspaceID: new(a.env.WorkspaceID),
		SubjectType: events.SubjectDomain,
		SubjectID:   new(primaryID),
		Data:        map[string]any{events.FieldPrimary: true},
	})
}
