package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	db "github.com/team-loco/loco/api/gen/db"
	planner "github.com/team-loco/loco/api/pkg/infra"
	"github.com/team-loco/loco/api/tvm"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func (s *InfrastructureServer) ApplyInfrastructure(
	ctx context.Context,
	req *connect.Request[infrav1.ApplyInfrastructureRequest],
) (*connect.Response[infrav1.ApplyInfrastructureResponse], error) {
	r := req.Msg
	planID, err := uuid.Parse(r.GetPlanId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	row, err := db.New(s.db).GetInfraPlan(ctx, planID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("infrastructure plan not found"))
	}
	if authorizeErr := s.authorize(ctx, row.EnvironmentID, db.ScopeWrite); authorizeErr != nil {
		return nil, authorizeErr
	}
	if stackErr := tvm.VerifyStackTarget(ctx, row.EnvironmentID, row.StackName); stackErr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, stackErr)
	}
	if r.GetManifestDigest() != row.ManifestDigest || r.GetSourceDigest() != row.SourceDigest {
		return nil, connect.NewError(
			connect.CodeFailedPrecondition,
			errors.New("plan inputs differ from the reviewed inputs"),
		)
	}
	plan := &infrav1.Plan{}
	if unmarshalErr := protojson.Unmarshal(row.Plan, plan); unmarshalErr != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if err = verifyReviewedPlan(plan, r.GetPlanDigest()); err != nil {
		return nil, err
	}
	if err = s.authorizeInfraChanges(ctx, row.EnvironmentID, plan, r.GetConfirmDestructive()); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	environment, err := q.LockInfraEnvironment(ctx, row.EnvironmentID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound)
	}
	previous, err := q.GetInfraApply(ctx, planID)
	if err == nil {
		return connect.NewResponse(infraApplyResponse(previous.ID, previous.DeploymentIds)), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if time.Now().After(row.ExpiresAt) {
		return nil, connect.NewError(
			connect.CodeFailedPrecondition,
			errors.New("infrastructure plan expired; create and review a new plan"),
		)
	}
	if environment.IntentRevision != row.ExpectedRevision {
		return nil, connect.NewError(
			connect.CodeAborted,
			errors.New("environment changed after planning; create and review a new plan"),
		)
	}
	payload, manifest, err := s.decodeInfraPlan(row)
	if err != nil {
		return nil, err
	}
	if preflightInfraErr := preflightInfra(ctx, q, environment, manifest, payload.Variables); preflightInfraErr != nil {
		return nil, preflightInfraErr
	}
	current, resources, err := s.infraSnapshot(ctx, q, row.EnvironmentID, row.StackName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	deployments := make([]uuid.UUID, 0)
	if len(plan.GetOperations()) > 0 {
		stack, ensureInfraStackErr := q.EnsureInfraStack(
			ctx,
			db.EnsureInfraStackParams{EnvironmentID: row.EnvironmentID, Name: row.StackName},
		)
		if ensureInfraStackErr != nil {
			return nil, connect.NewError(connect.CodeInternal, ErrDB)
		}
		for _, operation := range plan.GetOperations() {
			if operation.GetType() == infrav1.OperationType_OPERATION_TYPE_DELETE {
				if deleteErr := deleteInfraOwnedResource(
					ctx,
					q,
					resources,
					operation.GetServiceKey(),
				); deleteErr != nil {
					return nil, deleteErr
				}
				continue
			}
			service := findInfraService(manifest, operation.GetServiceKey())
			if err = verifyInfraService(service); err != nil {
				return nil, err
			}
			ids, applyInfraServiceErr := applyInfraService(ctx, q, environment, stack.ID, service, operation,
				resources[service.GetKey()], current[service.GetKey()], payload.Variables[service.GetKey()], s.cipher)
			if applyInfraServiceErr != nil {
				return nil, deploymentTxError(ctx, applyInfraServiceErr)
			}
			deployments = append(deployments, ids...)
		}
		if err = storeInfraManifest(ctx, q, stack, manifest, payload.Selector); err != nil {
			return nil, err
		}
	}
	id, err := q.StoreInfraApply(ctx, db.StoreInfraApplyParams{PlanID: planID, DeploymentIds: deployments})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return nil, deploymentTxError(ctx, commitErr)
	}
	return connect.NewResponse(infraApplyResponse(id, deployments)), nil
}

func infraApplyResponse(id uuid.UUID, deployments []uuid.UUID) *infrav1.ApplyInfrastructureResponse {
	result := &infrav1.ApplyInfrastructureResponse{ApplyId: id.String()}
	for _, deployment := range deployments {
		result.DeploymentIds = append(result.DeploymentIds, deployment.String())
	}
	return result
}

func findInfraService(manifest *infrav1.StackManifest, key string) *infrav1.ServiceManifest {
	for _, service := range manifest.GetServices() {
		if service.GetKey() == key {
			return service
		}
	}
	return nil
}

func mergeInfraManifest(stored []byte, desired *infrav1.StackManifest, selector string) ([]byte, error) {
	if selector == "" {
		return protojson.Marshal(desired)
	}
	merged := &infrav1.StackManifest{Version: desired.GetVersion(), Name: desired.GetName()}
	if len(stored) > 0 {
		if err := protojson.Unmarshal(stored, merged); err != nil {
			return nil, err
		}
	}
	services := make([]*infrav1.ServiceManifest, 0, len(merged.Services)+1)
	for _, service := range merged.GetServices() {
		if service.GetKey() != selector {
			services = append(services, service)
		}
	}
	services = append(services, findInfraService(desired, selector))
	merged.Services = services
	return protojson.Marshal(merged)
}

func applyInfraService(
	ctx context.Context,
	q *db.Queries,
	environment db.Environment,
	stackID uuid.UUID,
	service *infrav1.ServiceManifest,
	operation *infrav1.Operation,
	existing db.Resource,
	previous planner.CurrentService,
	variables map[string]string,
	cipher *planner.Cipher,
) ([]uuid.UUID, error) {
	specJSON, err := protojson.Marshal(service.GetSpec().GetService())
	if err != nil {
		return nil, err
	}
	resourceID := existing.ID
	if resourceID == uuid.Nil {
		resourceID, err = q.CreateResource(ctx, db.CreateResourceParams{
			WorkspaceID: environment.WorkspaceID, EnvironmentID: environment.ID, StackID: stackID,
			ServiceKey: service.GetKey(), Name: service.GetName(), Description: service.GetDescription(),
			Type: db.ResourceTypeService, Status: db.ResourceStatusUnavailable, Spec: specJSON, SpecVersion: 1,
		})
	} else {
		err = q.UpdateInfraResource(ctx, db.UpdateInfraResourceParams{
			ID: resourceID, Name: service.GetName(), Description: service.GetDescription(), Spec: specJSON,
		})
	}
	if err != nil {
		return nil, err
	}
	if err = storeResourceVariables(ctx, q, resourceID, variables, cipher); err != nil {
		return nil, err
	}
	if err = applyInfraRegions(ctx, q, resourceID, service, previous); err != nil {
		return nil, err
	}
	if err = applyInfraDomain(ctx, q, resourceID, service); err != nil {
		return nil, err
	}
	res, err := q.GetResourceByID(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	var deployments []uuid.UUID
	if !operation.GetDeploy() {
		return deployments, nil
	}
	force := slices.Contains(operation.GetChangedFields(), "spec") ||
		slices.Contains(operation.GetChangedFields(), "domain")
	for region := range service.GetSpec().GetService().GetRegions() {
		image := service.GetResolvedImage()
		live := previous.Runtime[region]
		if image == "" && live != nil {
			image = live.GetBuild().GetImage()
		}
		if image == "" {
			continue
		}
		runtime, runtimeSpecErr := planner.RuntimeSpec(service, region, image, variables)
		if runtimeSpecErr != nil {
			return nil, runtimeSpecErr
		}
		if !force && proto.Equal(runtime, live) {
			continue
		}
		cluster, runtimeSpecErr := q.GetActiveClusterByRegionAndTier(ctx, db.GetActiveClusterByRegionAndTierParams{
			Region: region, Tier: environment.EnvironmentType,
		})
		if runtimeSpecErr != nil {
			return nil, runtimeSpecErr
		}
		deploymentSpec := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: runtime}}
		stored := proto.Clone(runtime)
		redacted, ok := stored.(*deploymentv1.ServiceDeploymentSpec)
		if !ok {
			return nil, fmt.Errorf("clone deployment spec")
		}
		redacted.Env = nil
		data, runtimeSpecErr := protojson.Marshal(redacted)
		if runtimeSpecErr != nil {
			return nil, runtimeSpecErr
		}
		id, runtimeSpecErr := createDeploymentWithCleanup(ctx, q, db.CreateDeploymentParams{
			ResourceID: resourceID, ClusterID: cluster.ID, Region: region, Replicas: runtime.GetMinReplicas(),
			Status: db.DeploymentStatusPending, IsActive: true, Message: "Applying infrastructure plan",
			Spec: data, SpecVersion: 1, EnvironmentID: environment.ID,
		}, desiredApplicationSpec(res, service.GetSpec(), service.GetHostname(), deploymentSpec,
			region, environment.ID, environment.Name), cipher)
		if runtimeSpecErr != nil {
			return nil, runtimeSpecErr
		}
		deployments = append(deployments, id)
	}
	return deployments, nil
}

func applyInfraRegions(
	ctx context.Context,
	q *db.Queries,
	resourceID uuid.UUID,
	service *infrav1.ServiceManifest,
	previous planner.CurrentService,
) error {
	if resetInfraPrimaryRegionErr := q.ResetInfraPrimaryRegion(ctx, resourceID); resetInfraPrimaryRegionErr != nil {
		return resetInfraPrimaryRegionErr
	}
	for name, region := range service.GetSpec().GetService().GetRegions() {
		if _, upsertInfraRegionErr := q.UpsertInfraRegion(ctx, db.UpsertInfraRegionParams{
			ResourceID: resourceID, Region: name, IsPrimary: region.GetPrimary(),
		}); upsertInfraRegionErr != nil {
			return upsertInfraRegionErr
		}
	}
	for region := range previous.Runtime {
		if _, ok := service.GetSpec().GetService().GetRegions()[region]; !ok {
			active, getActiveDeploymentForResourceAndRegionErr := q.GetActiveDeploymentForResourceAndRegion(
				ctx,
				db.GetActiveDeploymentForResourceAndRegionParams{
					ResourceID: resourceID, Region: region,
				},
			)
			if getActiveDeploymentForResourceAndRegionErr != nil {
				return getActiveDeploymentForResourceAndRegionErr
			}
			if markDeploymentNotActiveErr := q.MarkDeploymentNotActive(
				ctx,
				active.ID,
			); markDeploymentNotActiveErr != nil {
				return markDeploymentNotActiveErr
			}
			if removePlacementErr := removePlacement(ctx, q, resourceID, active.ClusterID); removePlacementErr != nil {
				return removePlacementErr
			}
			if retireInfraRegionErr := q.RetireInfraRegion(
				ctx,
				db.RetireInfraRegionParams{ResourceID: resourceID, Region: region},
			); retireInfraRegionErr != nil {
				return retireInfraRegionErr
			}
		}
	}
	rows, err := q.ListResourceRegions(ctx, resourceID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, exists := service.GetSpec().GetService().GetRegions()[row.Region]; !exists {
			if err = q.RetireInfraRegion(
				ctx,
				db.RetireInfraRegionParams{ResourceID: resourceID, Region: row.Region},
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyInfraDomain(
	ctx context.Context,
	q *db.Queries,
	resourceID uuid.UUID,
	service *infrav1.ServiceManifest,
) error {
	if deleteInfraDomainsErr := q.DeleteInfraDomains(ctx, resourceID); deleteInfraDomainsErr != nil {
		return deleteInfraDomainsErr
	}
	if service.GetHostname() != "" {
		domainID, label, resolveInfraDomainErr := resolveInfraDomain(ctx, q, service.GetHostname())
		if resolveInfraDomainErr != nil {
			return resolveInfraDomainErr
		}
		if _, createResourceDomainErr := q.CreateResourceDomain(ctx, db.CreateResourceDomainParams{
			ResourceID: resourceID, Domain: service.GetHostname(), DomainSource: db.DomainSourcePlatformProvided,
			SubdomainLabel: &label, PlatformDomainID: &domainID, IsPrimary: true,
		}); createResourceDomainErr != nil {
			return createResourceDomainErr
		}
	}
	return nil
}

func (s *InfrastructureServer) authorizeInfraChanges(
	ctx context.Context,
	environmentID uuid.UUID,
	plan *infrav1.Plan,
	confirmed bool,
) error {
	for _, operation := range plan.GetOperations() {
		if operation.GetDestructive() {
			if !confirmed {
				return connect.NewError(
					connect.CodeFailedPrecondition,
					errors.New("destructive changes require explicit confirmation"),
				)
			}
			if authorizeErr2 := s.authorize(ctx, environmentID, db.ScopeAdmin); authorizeErr2 != nil {
				return authorizeErr2
			}
		}
	}
	return nil
}

func (s *InfrastructureServer) decodeInfraPlan(row db.InfraPlan) (infraPayload, *infrav1.StackManifest, error) {
	plaintext, err := s.cipher.Open(row.Payload, []byte(row.EnvironmentID.String()+"/"+row.StackName))
	if err != nil {
		return infraPayload{}, nil, connect.NewError(connect.CodeInternal, err)
	}
	var payload infraPayload
	if unmarshalErr2 := json.Unmarshal(plaintext, &payload); unmarshalErr2 != nil {
		return infraPayload{}, nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	manifest := &infrav1.StackManifest{}
	if unmarshalErr3 := protojson.Unmarshal(payload.Manifest, manifest); unmarshalErr3 != nil {
		return infraPayload{}, nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	return payload, manifest, nil
}

func verifyInfraService(service *infrav1.ServiceManifest) error {
	if service == nil {
		return connect.NewError(connect.CodeInternal, errors.New("stored plan has an unknown service"))
	}
	return nil
}

func storeInfraManifest(
	ctx context.Context,
	q *db.Queries,
	stack db.InfraStack,
	manifest *infrav1.StackManifest,
	selector string,
) error {
	safe, cloneErr := redactedInfraManifest(manifest)
	if cloneErr != nil {
		return connect.NewError(connect.CodeInternal, ErrDB)
	}
	stored, ensureInfraStackErr := mergeInfraManifest(stack.Manifest, safe, selector)
	if ensureInfraStackErr != nil {
		return connect.NewError(connect.CodeInternal, ErrDB)
	}
	if updateInfraStackManifestErr := q.UpdateInfraStackManifest(
		ctx,
		db.UpdateInfraStackManifestParams{ID: stack.ID, Manifest: stored},
	); updateInfraStackManifestErr != nil {
		return connect.NewError(connect.CodeInternal, ErrDB)
	}
	return nil
}

func verifyReviewedPlan(plan *infrav1.Plan, reviewedDigest string) error {
	digest, err := planner.Digest(plan)
	if err != nil {
		return connect.NewError(connect.CodeInternal, ErrDB)
	}
	if digest != reviewedDigest {
		return connect.NewError(
			connect.CodeFailedPrecondition,
			errors.New("reviewed plan differs from the stored plan"),
		)
	}
	return nil
}

func redactedInfraManifest(manifest *infrav1.StackManifest) (*infrav1.StackManifest, error) {
	copy, ok := proto.Clone(manifest).(*infrav1.StackManifest)
	if !ok {
		return nil, errors.New("invalid manifest clone")
	}
	for _, service := range copy.GetServices() {
		for name, variable := range service.GetVariables() {
			if _, literal := variable.GetExpression().(*infrav1.Variable_Literal); literal {
				service.Variables[name] = &infrav1.Variable{Expression: &infrav1.Variable_Preserve{Preserve: true}}
			}
		}
	}
	return copy, nil
}

func deleteInfraOwnedResource(ctx context.Context, q *db.Queries, resources map[string]db.Resource, key string) error {
	owned, exists := resources[key]
	if !exists {
		return connect.NewError(connect.CodeAborted, errors.New("owned resource disappeared after planning"))
	}
	if err := removeResourcePlacements(ctx, q, owned.ID); err != nil {
		return deploymentTxError(ctx, err)
	}
	if err := q.DeleteResource(ctx, owned.ID); err != nil {
		return connect.NewError(connect.CodeInternal, ErrDB)
	}
	return nil
}
