package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	planner "github.com/team-loco/loco/api/pkg/infra"
	"github.com/team-loco/loco/api/tvm"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	"github.com/team-loco/loco/gen/go/loco/infra/v1/infrav1connect"
	"github.com/team-loco/loco/internal/infra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/api/resource"
)

const infraProtocolVersion = 1

type InfrastructureServer struct {
	infrav1connect.UnimplementedInfrastructureServiceHandler
	db      *pgxpool.Pool
	machine *tvm.VendingMachine
	cipher  *planner.Cipher
}

type infraPayload struct {
	Manifest  []byte                       `json:"manifest"`
	Variables map[string]map[string]string `json:"variables"`
	Selector  string                       `json:"selector"`
}

func NewInfrastructureServer(
	pool *pgxpool.Pool,
	machine *tvm.VendingMachine,
	key string,
) (*InfrastructureServer, error) {
	cipher, err := planner.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &InfrastructureServer{db: pool, machine: machine, cipher: cipher}, nil
}

func (s *InfrastructureServer) authorize(ctx context.Context, environmentID uuid.UUID, scope db.Scope) error {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]db.EntityScope)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication is required"))
	}
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, db.EntityScope{
		EntityType: db.EntityTypeEnvironment, EntityID: environmentID, Scope: scope,
	}); err != nil {
		return connect.NewError(connect.CodePermissionDenied, err)
	}
	return nil
}

func (s *InfrastructureServer) PlanInfrastructure(
	ctx context.Context,
	req *connect.Request[infrav1.PlanInfrastructureRequest],
) (*connect.Response[infrav1.PlanInfrastructureResponse], error) {
	r := req.Msg
	environmentID, err := uuid.Parse(r.GetEnvironmentId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if authorizeErr := s.authorize(ctx, environmentID, db.ScopeRead); authorizeErr != nil {
		return nil, authorizeErr
	}
	if stackErr := tvm.VerifyStackTarget(ctx, environmentID, r.GetManifest().GetName()); stackErr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, stackErr)
	}
	input, err := selectedInfraManifest(r.GetManifest(), r.GetServiceKey())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err = infra.ValidateManifest(
		&infrav1.EvaluationManifest{Version: infraProtocolVersion, Stack: input},
	); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	normalized := proto.CloneOf(input)
	applyInfraDefaults(normalized)
	if err = infra.ValidateManifest(
		&infrav1.EvaluationManifest{Version: infraProtocolVersion, Stack: normalized},
	); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err = normalizeInfraImages(normalized, r.GetManifest()); err != nil {
		return nil, err
	}
	manifestDigest, err := planner.Digest(normalized)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	environment, err := q.LockInfraEnvironment(ctx, environmentID)
	if err != nil || environment.WorkspaceID.String() != r.GetWorkspaceId() {
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound)
	}
	current, _, err := s.infraSnapshot(ctx, q, environmentID, normalized.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	variables, err := s.resolveInfraVariables(ctx, q, environmentID, normalized, current)
	if err != nil {
		return nil, err
	}
	operations, err := planner.Operations(normalized, current, variables, r.GetServiceKey())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if preflightInfraErr := preflightInfra(ctx, q, environment, normalized, variables); preflightInfraErr != nil {
		return nil, preflightInfraErr
	}
	manifestJSON, err := protojson.Marshal(normalized)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	data, err := json.Marshal(infraPayload{Manifest: manifestJSON, Variables: variables, Selector: r.GetServiceKey()})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	identity := []byte(environmentID.String() + "/" + normalized.GetName())
	encrypted, err := s.cipher.Seal(data, identity)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	plan := &infrav1.Plan{
		WorkspaceId: r.GetWorkspaceId(), EnvironmentId: environmentID.String(), StackName: normalized.GetName(),
		ExpectedRevision: environment.IntentRevision, SourceDigest: r.GetSourceDigest(), ManifestDigest: manifestDigest,
		Operations: operations, ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)),
	}
	planJSON, err := protojson.Marshal(plan)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	id, err := q.StoreInfraPlan(ctx, db.StoreInfraPlanParams{
		WorkspaceID: environment.WorkspaceID, EnvironmentID: environmentID, StackName: plan.StackName,
		ExpectedRevision: environment.IntentRevision, ManifestDigest: manifestDigest, SourceDigest: r.GetSourceDigest(),
		Payload: encrypted, Plan: planJSON, ExpiresAt: plan.ExpiresAt.AsTime(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	plan.Id = id.String()
	planJSON, err = protojson.Marshal(plan)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if updateInfraPlanProjectionErr := q.UpdateInfraPlanProjection(
		ctx,
		db.UpdateInfraPlanProjectionParams{ID: id, Plan: planJSON},
	); updateInfraPlanProjectionErr != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	return connect.NewResponse(&infrav1.PlanInfrastructureResponse{Plan: plan}), nil
}

func validateInfraResources(service *infrav1.ServiceManifest) error {
	for name, region := range service.GetSpec().GetService().GetRegions() {
		for _, value := range []string{region.GetCpu(), region.GetMemory()} {
			quantity, err := resource.ParseQuantity(value)
			if err != nil || quantity.Sign() <= 0 {
				return fmt.Errorf("region %q requires positive resource quantities", name)
			}
		}
		if region.GetMaxReplicas() > 3 {
			return fmt.Errorf("region %q exceeds the maximum of 3 replicas", name)
		}
	}
	return nil
}

func (s *InfrastructureServer) infraSnapshot(
	ctx context.Context,
	q *db.Queries,
	environmentID uuid.UUID,
	name string,
) (map[string]planner.CurrentService, map[string]db.Resource, error) {
	current := make(map[string]planner.CurrentService)
	resources := make(map[string]db.Resource)
	stack, err := q.GetInfraStack(ctx, db.GetInfraStackParams{EnvironmentID: environmentID, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return current, resources, nil
	}
	if err != nil {
		return nil, nil, err
	}
	stored := &infrav1.StackManifest{}
	if len(stack.Manifest) > 0 {
		if unmarshalErr := protojson.Unmarshal(stack.Manifest, stored); unmarshalErr != nil {
			return nil, nil, unmarshalErr
		}
	}
	recipes := make(map[string]*infrav1.ServiceManifest)
	for _, service := range stored.GetServices() {
		recipes[service.GetKey()] = service
	}
	rows, err := q.ListInfraStackResources(ctx, stack.ID)
	if err != nil {
		return nil, nil, err
	}
	for _, row := range rows {
		spec, deserializeResourceSpecErr := converter.DeserializeResourceSpec(row.Spec, row.Type)
		if deserializeResourceSpecErr != nil {
			return nil, nil, deserializeResourceSpecErr
		}
		manifest := &infrav1.ServiceManifest{
			Key:         row.ServiceKey,
			Name:        row.Name,
			Description: row.Description,
			Spec:        spec,
		}
		if recipe := recipes[row.ServiceKey]; recipe != nil {
			manifest.Source = recipe.GetSource()
			manifest.Variables = recipe.GetVariables()
		}
		domain, deserializeResourceSpecErr := q.GetDomainByResourceId(ctx, row.ID)
		if deserializeResourceSpecErr != nil && !errors.Is(deserializeResourceSpecErr, pgx.ErrNoRows) {
			return nil, nil, deserializeResourceSpecErr
		}
		manifest.Hostname = domain.Domain
		live := planner.CurrentService{
			Manifest: manifest,
			Runtime:  make(map[string]*deploymentv1.ServiceDeploymentSpec),
		}
		storedValues, valuesErr := loadResourceVariables(row, s.cipher)
		if valuesErr != nil {
			return nil, nil, valuesErr
		}
		live.Variables = storedValues
		deployments, deserializeResourceSpecErr := q.ListActiveDeploymentsForResource(ctx, row.ID)
		if deserializeResourceSpecErr != nil {
			return nil, nil, deserializeResourceSpecErr
		}
		for _, deployment := range deployments {
			if deployment.EnvironmentID != environmentID {
				return nil, nil, errors.New("resource has a deployment in a different environment")
			}
			spec, deserializeDeploymentSpecErr := converter.DeserializeDeploymentSpec(deployment.Spec, string(row.Type))
			if deserializeDeploymentSpecErr != nil {
				return nil, nil, deserializeDeploymentSpecErr
			}
			env, deserializeDeploymentSpecErr := desiredEnv(ctx, q, row.ID, deployment.ClusterID, s.cipher)
			if deserializeDeploymentSpecErr != nil {
				return nil, nil, deserializeDeploymentSpecErr
			}
			spec.GetService().Env = env
			live.Runtime[deployment.Region] = spec.GetService()
			if deployment.ID == deployments[0].ID {
				live.Variables = maps.Clone(env)
			}
		}
		current[row.ServiceKey] = live
		resources[row.ServiceKey] = row
	}
	return current, resources, nil
}

func (s *InfrastructureServer) resolveInfraVariables(
	ctx context.Context,
	q *db.Queries,
	environmentID uuid.UUID,
	manifest *infrav1.StackManifest,
	current map[string]planner.CurrentService,
) (map[string]map[string]string, error) {
	resolved := make(map[string]map[string]string, len(manifest.GetServices()))
	for _, service := range manifest.GetServices() {
		values := make(map[string]string, len(service.GetVariables()))
		for name, variable := range service.GetVariables() {
			switch expression := variable.GetExpression().(type) {
			case *infrav1.Variable_Literal:
				values[name] = expression.Literal
			case *infrav1.Variable_Preserve:
				value, ok := current[service.GetKey()].Variables[name]
				if !ok {
					return nil, connect.NewError(
						connect.CodeFailedPrecondition,
						fmt.Errorf("variable %q has no value to preserve", name),
					)
				}
				if err := verifyPreservedVariable(current[service.GetKey()], name, value); err != nil {
					return nil, err
				}
				values[name] = value
			case *infrav1.Variable_Secret:
				version, err := q.GetLatestInfraSecretVersion(ctx, db.GetLatestInfraSecretVersionParams{
					EnvironmentID: environmentID, Name: expression.Secret,
				})
				if err != nil {
					return nil, connect.NewError(
						connect.CodeFailedPrecondition,
						fmt.Errorf("secret %q is unavailable", expression.Secret),
					)
				}
				plaintext, err := s.cipher.Open(
					version.Ciphertext,
					[]byte(environmentID.String()+"/secret/"+expression.Secret),
				)
				if err != nil {
					return nil, connect.NewError(connect.CodeInternal, errors.New("secret cannot be decrypted"))
				}
				values[name] = string(plaintext)
			default:
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported variable expression"))
			}
		}
		resolved[service.GetKey()] = values
	}
	return resolved, nil
}

func preflightInfra(
	ctx context.Context,
	q *db.Queries,
	environment db.Environment,
	manifest *infrav1.StackManifest,
	variables map[string]map[string]string,
) error {
	for _, service := range manifest.GetServices() {
		for region := range service.GetSpec().GetService().GetRegions() {
			image := service.GetResolvedImage()
			if image == "" {
				image = "loco/preflight@sha256:" + strings.Repeat("0", 64)
			}
			runtime, runtimeErr := planner.RuntimeSpec(service, region, image, variables[service.GetKey()])
			if runtimeErr != nil {
				return connect.NewError(connect.CodeInvalidArgument, runtimeErr)
			}
			plannedResource := db.Resource{
				ID:          uuid.New(),
				WorkspaceID: environment.WorkspaceID,
				Name:        service.GetName(),
				Type:        db.ResourceTypeService,
			}
			spec := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: runtime}}
			if _, validateErr := buildApplicationSpec(
				plannedResource,
				service.GetSpec(),
				service.GetHostname(),
				spec,
				region,
				environment.ID,
				environment.Name,
				uuid.New(),
			); validateErr != nil {
				return connect.NewError(
					connect.CodeInvalidArgument,
					fmt.Errorf("service %q region %q: %w", service.GetKey(), region, validateErr),
				)
			}
			if _, err := q.GetActiveClusterByRegionAndTier(ctx, db.GetActiveClusterByRegionAndTierParams{
				Region: region, Tier: environment.EnvironmentType,
			}); err != nil {
				return connect.NewError(
					connect.CodeFailedPrecondition,
					fmt.Errorf("no healthy cluster for region %q and environment tier", region),
				)
			}
		}
		if service.GetHostname() != "" {
			if _, _, err := resolveInfraDomain(ctx, q, service.GetHostname()); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveInfraDomain(ctx context.Context, q *db.Queries, hostname string) (uuid.UUID, string, error) {
	domains, err := q.ListActivePlatformDomains(ctx)
	if err != nil {
		return uuid.Nil, "", connect.NewError(connect.CodeInternal, ErrDB)
	}
	var selected db.PlatformDomain
	for _, domain := range domains {
		if strings.HasSuffix(hostname, "."+domain.Domain) && len(domain.Domain) > len(selected.Domain) {
			selected = domain
		}
	}
	if selected.ID == uuid.Nil {
		return uuid.Nil, "", connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("hostname does not match an active platform domain"),
		)
	}
	label := strings.TrimSuffix(hostname, "."+selected.Domain)
	if strings.Contains(label, ".") {
		return uuid.Nil, "", connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("platform hostname requires one subdomain label"),
		)
	}
	return selected.ID, label, nil
}

var infraSecretName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func (s *InfrastructureServer) SetSecret(
	ctx context.Context,
	req *connect.Request[infrav1.SetSecretRequest],
) (*connect.Response[infrav1.SetSecretResponse], error) {
	r := req.Msg
	environmentID, err := uuid.Parse(r.GetEnvironmentId())
	if err != nil || !infraSecretName.MatchString(r.GetName()) ||
		!utf8.Valid(r.GetValue()) || bytes.IndexByte(r.GetValue(), 0) >= 0 {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("valid environment and secret name are required"),
		)
	}
	if authorizeErr2 := s.authorize(ctx, environmentID, db.ScopeWrite); authorizeErr2 != nil {
		return nil, authorizeErr2
	}
	if tvm.IsStackRestricted(ctx) {
		return nil, connect.NewError(
			connect.CodePermissionDenied,
			errors.New("stack credentials cannot modify shared environment secrets"),
		)
	}
	ciphertext, err := s.cipher.Seal(r.GetValue(), []byte(environmentID.String()+"/secret/"+r.GetName()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	id, err := db.New(s.db).CreateInfraSecretVersion(ctx, db.CreateInfraSecretVersionParams{
		EnvironmentID: environmentID, Name: r.GetName(), Ciphertext: ciphertext,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	return connect.NewResponse(&infrav1.SetSecretResponse{VersionId: id.String()}), nil
}

func (s *InfrastructureServer) GetStack(
	ctx context.Context,
	req *connect.Request[infrav1.GetStackRequest],
) (*connect.Response[infrav1.GetStackResponse], error) {
	r := req.Msg
	environmentID, err := uuid.Parse(r.GetEnvironmentId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if authorizeErr3 := s.authorize(ctx, environmentID, db.ScopeRead); authorizeErr3 != nil {
		return nil, authorizeErr3
	}
	if stackErr := tvm.VerifyStackTarget(ctx, environmentID, r.GetName()); stackErr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, stackErr)
	}
	q := db.New(s.db)
	env, err := q.GetEnvironmentByID(ctx, environmentID)
	if err != nil || env.WorkspaceID.String() != r.GetWorkspaceId() {
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound)
	}
	stack, err := q.GetInfraStack(ctx, db.GetInfraStackParams{EnvironmentID: environmentID, Name: r.GetName()})
	if err != nil || len(stack.Manifest) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("stack has no infrastructure definition"))
	}
	manifest := &infrav1.StackManifest{}
	if unmarshalErr2 := protojson.Unmarshal(stack.Manifest, manifest); unmarshalErr2 != nil {
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	for _, service := range manifest.GetServices() {
		service.ResolvedImage = ""
		for name, variable := range service.GetVariables() {
			if _, ok := variable.GetExpression().(*infrav1.Variable_Literal); ok {
				service.Variables[name] = &infrav1.Variable{Expression: &infrav1.Variable_Preserve{Preserve: true}}
			}
		}
	}
	return connect.NewResponse(&infrav1.GetStackResponse{Manifest: manifest}), nil
}

func selectedInfraManifest(manifest *infrav1.StackManifest, selector string) (*infrav1.StackManifest, error) {
	if selector == "" {
		return manifest, nil
	}
	selected := &infrav1.StackManifest{Version: manifest.GetVersion(), Name: manifest.GetName()}
	for _, service := range manifest.GetServices() {
		if service.GetKey() == selector {
			selected.Services = append(selected.Services, service)
		}
	}
	if len(selected.Services) == 0 {
		return nil, fmt.Errorf("service key %q is not declared", selector)
	}
	return selected, nil
}

func normalizeInfraImages(normalized, originalManifest *infrav1.StackManifest) error {
	for _, service := range normalized.GetServices() {
		for _, original := range originalManifest.GetServices() {
			if original.GetKey() == service.GetKey() {
				service.ResolvedImage = original.GetResolvedImage()
				if service.ResolvedImage == "" && strings.Contains(service.GetImage(), "@sha256:") {
					service.ResolvedImage = service.GetImage()
				}
			}
		}
		if service.GetResolvedImage() != "" {
			if validateImageErr := planner.ValidateImage(service.GetResolvedImage()); validateImageErr != nil {
				return connect.NewError(connect.CodeInvalidArgument, validateImageErr)
			}
		}
		if validateInfraResourcesErr := validateInfraResources(service); validateInfraResourcesErr != nil {
			return connect.NewError(connect.CodeInvalidArgument, validateInfraResourcesErr)
		}
	}
	return nil
}

func verifyPreservedVariable(service planner.CurrentService, name, value string) error {
	for region, runtime := range service.Runtime {
		current, exists := runtime.GetEnv()[name]
		if !exists || current != value {
			return connect.NewError(
				connect.CodeFailedPrecondition,
				fmt.Errorf(
					"variable %q differs across regions; supply a literal or secret reference for %q",
					name,
					region,
				),
			)
		}
	}
	return nil
}
