package service

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/infra"
	loco "github.com/team-loco/loco/sdk/go"
	"google.golang.org/protobuf/encoding/protojson"
)

type infrastructureFixture struct {
	deployment *deployFixture
	server     *InfrastructureServer
	ctx        context.Context
	workspace  uuid.UUID
}

const infraTestStack = "storefront"

const infrastructureTestRegion = "us-east-1"

const infraTestServiceKey = "api"

func newInfrastructureFixture(t *testing.T) *infrastructureFixture {
	t.Helper()
	f := newDeployFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, "UPDATE clusters SET tier = 'production', health_status = 'healthy'"); err != nil {
		t.Fatal(err)
	}
	environment, err := f.queries.GetEnvironmentByID(ctx, f.envID)
	if err != nil {
		t.Fatal(err)
	}
	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{MaxAPITokenDuration: time.Hour})
	t.Cleanup(machine.Close)
	ctx = context.WithValue(ctx, contextkeys.EntityScopesKey, []db.EntityScope{
		{EntityType: db.EntityTypeEnvironment, EntityID: f.envID, Scope: db.ScopeRead},
		{EntityType: db.EntityTypeEnvironment, EntityID: f.envID, Scope: db.ScopeWrite},
		{EntityType: db.EntityTypeEnvironment, EntityID: f.envID, Scope: db.ScopeAdmin},
	})
	return &infrastructureFixture{
		deployment: f, server: &InfrastructureServer{db: f.pool, machine: machine, cipher: f.cipher},
		ctx: ctx, workspace: environment.WorkspaceID,
	}
}

func infraTestManifest(t *testing.T, keys ...string) *infrav1.StackManifest {
	t.Helper()
	manifest := &loco.Manifest{Version: loco.ProtocolVersion, Stack: loco.Stack{Name: infraTestStack}}
	image := "registry.example.com/storefront/api@sha256:" + strings.Repeat("a", 64)
	for _, key := range keys {
		manifest.Stack.Services = append(manifest.Stack.Services, loco.Service{
			Key: key, Image: image, Routing: loco.Routing{Port: 8000},
			PrimaryRegion: infrastructureTestRegion, Health: loco.Health{Path: "/health"},
			Regions: map[string]loco.Region{
				infrastructureTestRegion: {CPU: "100m", Memory: "256Mi", ReplicasMin: 1, ReplicasMax: 1},
			},
		})
	}
	result, err := infra.ProtoManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range result.GetServices() {
		service.ResolvedImage = image
	}
	return result
}

func (f *infrastructureFixture) plan(t *testing.T, manifest *infrav1.StackManifest) *infrav1.Plan {
	t.Helper()
	response, err := f.server.PlanInfrastructure(f.ctx, connect.NewRequest(&infrav1.PlanInfrastructureRequest{
		WorkspaceId: f.workspace.String(), EnvironmentId: f.deployment.envID.String(),
		Manifest: manifest, SourceDigest: strings.Repeat("b", 64),
	}))
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg.GetPlan()
}

func (f *infrastructureFixture) apply(
	plan *infrav1.Plan,
	destructive bool,
) (*infrav1.ApplyInfrastructureResponse, error) {
	digest, digestErr := infra.PlanDigest(plan)
	if digestErr != nil {
		return nil, digestErr
	}
	response, err := f.server.ApplyInfrastructure(f.ctx, connect.NewRequest(&infrav1.ApplyInfrastructureRequest{
		PlanId:             plan.GetId(),
		ManifestDigest:     plan.GetManifestDigest(),
		SourceDigest:       plan.GetSourceDigest(),
		PlanDigest:         digest,
		ConfirmDestructive: destructive,
	}))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func TestInfrastructurePlanIsReadOnlyAndApplyIsIdempotent(t *testing.T) {
	f := newInfrastructureFixture(t)
	manifest := infraTestManifest(t, infraTestServiceKey)
	plan := f.plan(t, manifest)
	if len(plan.GetOperations()) != 1 || !plan.GetOperations()[0].GetDeploy() {
		t.Fatalf("unexpected operations: %v", plan.GetOperations())
	}
	if _, err := f.deployment.queries.GetInfraStack(f.ctx, db.GetInfraStackParams{
		EnvironmentID: f.deployment.envID, Name: infraTestStack,
	}); err == nil {
		t.Fatal("planning created infrastructure")
	}
	first, err := f.apply(plan, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.GetDeploymentIds()) != 1 {
		t.Fatalf("scheduled deployments: %v", first.GetDeploymentIds())
	}
	second, err := f.apply(plan, false)
	if err != nil || first.GetApplyId() != second.GetApplyId() ||
		first.GetDeploymentIds()[0] != second.GetDeploymentIds()[0] {
		t.Fatalf("retry duplicated apply: %v, %v", second, err)
	}
	next := f.plan(t, manifest)
	if len(next.GetOperations()) != 0 {
		t.Fatalf("unchanged infrastructure produces operations: %v", next.GetOperations())
	}
}

func TestInfrastructureRejectsStaleAndModifiedPlans(t *testing.T) {
	f := newInfrastructureFixture(t)
	plan := f.plan(t, infraTestManifest(t, infraTestServiceKey))
	original := plan.SourceDigest
	plan.SourceDigest = strings.Repeat("c", 64)
	if _, err := f.apply(plan, false); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("modified plan accepted: %v", err)
	}
	plan.SourceDigest = original
	if _, err := f.deployment.queries.UpdateResource(f.ctx, db.UpdateResourceParams{
		ID: f.deployment.resourceID, Name: loco.Value("renamed"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.apply(plan, false); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale plan accepted: %v", err)
	}
}

func TestInfrastructureStatusUpdatesDoNotInvalidatePlans(t *testing.T) {
	f := newInfrastructureFixture(t)
	plan := f.plan(t, infraTestManifest(t, infraTestServiceKey))
	if err := f.deployment.queries.UpdateResourceStatus(f.ctx, db.UpdateResourceStatusParams{
		ID: f.deployment.resourceID, Status: db.ResourceStatusDegraded,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.apply(plan, false); err != nil {
		t.Fatalf("status observation invalidated plan: %v", err)
	}
}

func TestInfrastructureSecretsAreEncryptedAndPlansAreRedacted(t *testing.T) {
	f := newInfrastructureFixture(t)
	const secret = "private-secret-value-do-not-log"
	if _, err := f.server.SetSecret(f.ctx, connect.NewRequest(&infrav1.SetSecretRequest{
		EnvironmentId: f.deployment.envID.String(), Name: "database-url", Value: []byte(secret),
	})); err != nil {
		t.Fatal(err)
	}
	manifest := infraTestManifest(t, infraTestServiceKey)
	manifest.Services[0].Variables = map[string]*infrav1.Variable{
		"DATABASE_URL": {Expression: &infrav1.Variable_Secret{Secret: "database-url"}},
	}
	plan := f.plan(t, manifest)
	encoded, err := protojson.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(secret)) {
		t.Fatal("secret appeared in redacted plan")
	}
	stored, err := f.deployment.queries.GetInfraPlan(f.ctx, uuid.MustParse(plan.GetId()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored.Payload, []byte(secret)) {
		t.Fatal("plan stores plaintext secret")
	}
	response, err := f.apply(plan, false)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := f.deployment.queries.GetDeploymentByID(f.ctx, uuid.MustParse(response.GetDeploymentIds()[0]))
	if err != nil {
		t.Fatal(err)
	}
	placement, err := f.deployment.queries.GetPlacementForResourceCluster(
		f.ctx,
		db.GetPlacementForResourceClusterParams{
			ResourceID: deployment.ResourceID, ClusterID: deployment.ClusterID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(placement.DesiredSpec, []byte(secret)) || bytes.Contains(deployment.Spec, []byte(secret)) {
		t.Fatal("runtime persistence contains plaintext secret")
	}
	values, err := desiredEnv(
		f.ctx,
		f.deployment.queries,
		deployment.ResourceID,
		deployment.ClusterID,
		f.deployment.cipher,
	)
	if err != nil || values["DATABASE_URL"] != secret {
		t.Fatal("secret was not preserved for the agent")
	}
}

func TestInfrastructureRollbackWhenAnOperationFails(t *testing.T) {
	f := newInfrastructureFixture(t)
	manifest := infraTestManifest(t, infraTestServiceKey, "svc")
	plan := f.plan(t, manifest)
	if _, err := f.apply(plan, false); err == nil {
		t.Fatal("expected the foreign-stack name collision to fail")
	}
	var count int
	if err := f.deployment.pool.QueryRow(f.ctx, "SELECT count(*) FROM resources WHERE name = 'api'").
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed apply committed its first service")
	}
}

func TestInfrastructurePruneRequiresConfirmationAndKeepsOtherStacks(t *testing.T) {
	f := newInfrastructureFixture(t)
	if _, err := f.apply(f.plan(t, infraTestManifest(t, infraTestServiceKey)), false); err != nil {
		t.Fatal(err)
	}
	plan := f.plan(t, infraTestManifest(t))
	if _, err := f.apply(plan, false); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unconfirmed deletion accepted: %v", err)
	}
	if _, err := f.apply(plan, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.deployment.queries.GetResourceByID(f.ctx, f.deployment.resourceID); err != nil {
		t.Fatal("prune touched another stack")
	}
}

func TestInfrastructureStackCredentialCannotCrossOwnership(t *testing.T) {
	f := newInfrastructureFixture(t)
	restriction := &contextkeys.StackRestriction{EnvironmentID: f.deployment.envID, Name: infraTestStack}
	restricted := context.WithValue(f.ctx, contextkeys.StackRestrictionKey, restriction)
	manifest := infraTestManifest(t, infraTestServiceKey)
	request := &infrav1.PlanInfrastructureRequest{
		WorkspaceId:   f.workspace.String(),
		EnvironmentId: f.deployment.envID.String(),
		Manifest:      manifest,
		SourceDigest:  strings.Repeat("a", 64),
	}
	if _, err := f.server.PlanInfrastructure(restricted, connect.NewRequest(request)); err != nil {
		t.Fatal(err)
	}
	manifest.Name = "foreign"
	if _, err := f.server.PlanInfrastructure(
		restricted,
		connect.NewRequest(request),
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("cross-stack plan accepted: %v", err)
	}
	if _, err := f.server.SetSecret(
		restricted,
		connect.NewRequest(
			&infrav1.SetSecretRequest{
				EnvironmentId: f.deployment.envID.String(),
				Name:          "shared",
				Value:         []byte("value"),
			},
		),
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("shared secret mutation accepted: %v", err)
	}
	scopes := []db.EntityScope{
		{EntityType: db.EntityTypeEnvironment, EntityID: f.deployment.envID, Scope: db.ScopeWrite},
	}
	err := f.server.machine.VerifyWithGivenEntityScopes(
		restricted,
		scopes,
		db.EntityScope{EntityType: db.EntityTypeResource, EntityID: f.deployment.resourceID, Scope: db.ScopeWrite},
	)
	if err == nil {
		t.Fatal("stack credential authorized a foreign resource")
	}
}

func TestInfrastructureSameServiceHasIndependentEnvironmentInstances(t *testing.T) {
	f := newInfrastructureFixture(t)
	manifest := infraTestManifest(t, infraTestServiceKey)
	if _, err := f.apply(f.plan(t, manifest), false); err != nil {
		t.Fatal(err)
	}
	environment, err := f.deployment.queries.GetEnvironmentByID(f.ctx, f.deployment.envID)
	if err != nil {
		t.Fatal(err)
	}
	var second uuid.UUID
	err = f.deployment.pool.QueryRow(f.ctx, `INSERT INTO environments (workspace_id, name, environment_type, created_by)
 VALUES ($1, 'second-production', 'production', $2) RETURNING id`, f.workspace, environment.CreatedBy).Scan(&second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(
		f.ctx,
		contextkeys.EntityScopesKey,
		[]db.EntityScope{
			{EntityType: db.EntityTypeEnvironment, EntityID: second, Scope: db.ScopeRead},
			{EntityType: db.EntityTypeEnvironment, EntityID: second, Scope: db.ScopeWrite},
		},
	)
	planned, err := f.server.PlanInfrastructure(
		ctx,
		connect.NewRequest(
			&infrav1.PlanInfrastructureRequest{
				WorkspaceId:   f.workspace.String(),
				EnvironmentId: second.String(),
				Manifest:      manifest,
				SourceDigest:  strings.Repeat("b", 64),
			},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := planned.Msg.GetPlan()
	digest, digestErr := infra.PlanDigest(plan)
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	if _, err = f.server.ApplyInfrastructure(
		ctx,
		connect.NewRequest(
			&infrav1.ApplyInfrastructureRequest{
				PlanId:         plan.GetId(),
				SourceDigest:   plan.GetSourceDigest(),
				ManifestDigest: plan.GetManifestDigest(),
				PlanDigest:     digest,
			},
		),
	); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.deployment.pool.QueryRow(f.ctx, "SELECT count(DISTINCT id) FROM resources WHERE name = 'api'").
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("environment instances were shared: %d", count)
	}
}

func TestInfrastructureSelectedServicePreservesSiblings(t *testing.T) {
	f := newInfrastructureFixture(t)
	if _, err := f.apply(f.plan(t, infraTestManifest(t, infraTestServiceKey, "worker")), false); err != nil {
		t.Fatal(err)
	}
	manifest := infraTestManifest(t, infraTestServiceKey)
	manifest.Services[0].Description = "Selected change"
	result, err := f.server.PlanInfrastructure(
		f.ctx,
		connect.NewRequest(
			&infrav1.PlanInfrastructureRequest{
				WorkspaceId:   f.workspace.String(),
				EnvironmentId: f.deployment.envID.String(),
				Manifest:      manifest,
				SourceDigest:  strings.Repeat("a", 64),
				ServiceKey:    infraTestServiceKey,
			},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := result.Msg.GetPlan()
	for _, operation := range plan.GetOperations() {
		if operation.GetServiceKey() != infraTestServiceKey {
			t.Fatal("selected plan changed a sibling")
		}
	}
	if _, err = f.apply(plan, false); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.deployment.pool.QueryRow(f.ctx, "SELECT count(*) FROM resources WHERE name = 'worker'").
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("selected apply pruned its sibling")
	}
}

func TestInfrastructureApplyRejectsChangedReviewedOperations(t *testing.T) {
	f := newInfrastructureFixture(t)
	plan := f.plan(t, infraTestManifest(t, infraTestServiceKey))
	plan.Operations[0].ChangedFields = []string{"altered review"}
	if _, err := f.apply(plan, false); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("changed reviewed operations accepted: %v", err)
	}
}

func TestInfrastructurePreservesEncryptedVariablesBeforeFirstDeployment(t *testing.T) {
	f := newInfrastructureFixture(t)
	manifest := infraTestManifest(t, infraTestServiceKey)
	service := manifest.Services[0]
	service.Source = &infrav1.ServiceManifest_Docker{
		Docker: &infrav1.DockerSource{Context: ".", Dockerfile: "Dockerfile"},
	}
	service.ResolvedImage = ""
	service.Variables = map[string]*infrav1.Variable{
		"CONFIG_VALUE": {Expression: &infrav1.Variable_Literal{Literal: "private-unreleased-value"}},
	}
	result, err := f.apply(f.plan(t, manifest), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.GetDeploymentIds()) != 0 {
		t.Fatal("infrastructure-only apply created a deployment")
	}
	row, err := f.deployment.queries.GetResourceByNameAndWorkspace(
		f.ctx,
		db.GetResourceByNameAndWorkspaceParams{EnvironmentID: f.deployment.envID, Name: infraTestServiceKey},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(row.VariableValues, []byte("private-unreleased-value")) {
		t.Fatal("resource stores plaintext variable values")
	}
	stored, err := f.deployment.queries.GetInfraStack(
		f.ctx,
		db.GetInfraStackParams{EnvironmentID: f.deployment.envID, Name: infraTestStack},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored.Manifest, []byte("private-unreleased-value")) {
		t.Fatal("stack manifest stores plaintext variable values")
	}
	service.Variables["CONFIG_VALUE"] = &infrav1.Variable{Expression: &infrav1.Variable_Preserve{Preserve: true}}
	preserved := f.plan(t, manifest)
	if len(preserved.GetOperations()) != 0 {
		t.Fatal("preserve changed an undeployed service")
	}
	resources := NewResourceServer(f.deployment.pool, f.deployment.queries, f.server.machine, f.deployment.cipher)
	response, err := resources.GetResource(
		f.ctx,
		connect.NewRequest(
			&resourcev1.GetResourceRequest{Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: row.ID.String()}},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Msg.GetResource().GetVariableKeys()) != 1 ||
		response.Msg.GetResource().GetVariableKeys()[0] != "CONFIG_VALUE" {
		t.Fatal("undeployed variable names are missing")
	}
}
