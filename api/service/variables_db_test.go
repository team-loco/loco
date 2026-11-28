package service

import (
	"context"
	"encoding/hex"
	"errors"
	"maps"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/varref"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	referencedService = "api"
	referencedPort    = 9090
	referencedDomain  = "api.example.com"
	passwordName      = "PW"
	plaintextValue    = "pw-plaintext-7f3a91"
	dbHostName        = "DB_HOST"
	dbHostValue       = "db.internal"
	apiURLName        = "API_URL"
	releaseMode       = "release"
)

// referencedResource records a deployment of another service on referencedPort with the variables env.
func (f *deployFixture) referencedResource(t *testing.T, env map[string]string, resolved varref.Resolved) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := f.otherResource(t, referencedService)
	spec, err := protojson.Marshal(&deploymentv1.ServiceDeploymentSpec{
		Platform: "linux/amd64", Port: new(int32(referencedPort)), Env: env,
	})
	if err != nil {
		t.Fatal(err)
	}
	params := f.paramsFor(f.clusterID)
	params.ResourceID = id
	params.Spec = spec
	params.Resolved = resolved
	err = withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		_, deployErr := recordTestDeployment(ctx, qtx, limits.Install(limits.Defaults()), params, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy %s: %v", referencedService, err)
	}
	insert := `INSERT INTO resource_domains (resource_id, environment_id, domain, domain_source, is_primary)
VALUES ($1, $2, $3, 'custom', true)`
	if _, err = f.pool.Exec(ctx, insert, id, f.envID, referencedDomain); err != nil {
		t.Fatalf("domain of %s: %v", referencedService, err)
	}
	return id
}

// createDeploymentReadingSecrets is createDeploymentWith for a caller that can also list the
// workspace's secrets.
func createDeploymentReadingSecrets(
	t *testing.T,
	f *deployFixture,
	resourceSpec string,
	service *deploymentv1.ServiceDeploymentSpec,
) (*deploymentv1.CreateDeploymentResponse, error) {
	t.Helper()
	seedDeployment(t, f, resourceSpec)
	return deployRequest(t, f, &deploymentv1.CreateDeploymentRequest{
		ResourceId:    f.resourceID.String(),
		Regions:       []string{testRegion},
		EnvironmentId: f.envID.String(),
		Spec:          &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
	}, f.workspaceReadScopes(t)...)
}

func plainResolution(env map[string]string) varref.Resolved {
	return varref.Resolved{Plain: env, Secret: map[string]string{}}
}

func ref(source, key string) string {
	return varref.Ref{Source: source, Key: key}.String()
}

// deployVariables deploys the fixture's service with env as a caller that can read referenced
// and the workspace's secrets.
func (f *deployFixture) deployVariables(
	t *testing.T,
	referenced uuid.UUID,
	env map[string]string,
) (*deploymentv1.CreateDeploymentResponse, error) {
	t.Helper()
	seedDeployment(t, f, sameRegionSpec)
	service := imageService()
	service.Env = env
	return deployRequest(t, f, &deploymentv1.CreateDeploymentRequest{
		ResourceId:    f.resourceID.String(),
		Regions:       []string{testRegion},
		EnvironmentId: f.envID.String(),
		Spec:          &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
	}, append(
		f.workspaceReadScopes(t),
		genDb.EntityScope{EntityType: genDb.EntityTypeResource, EntityID: referenced, Scope: genDb.ScopeRead},
	)...)
}

func TestCreateDeploymentResolvesReferencesToSecretsAndOtherResources(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	f.setSecrets(t, provider, map[string]string{passwordName: plaintextValue})
	apiEnv := map[string]string{dbHostName: dbHostValue}
	api := f.referencedResource(t, apiEnv, plainResolution(apiEnv))

	resp, err := f.deployVariables(t, api, map[string]string{
		buildModeKey:   releaseMode,
		apiURLName:     ref(referencedService, varref.PrivateURL),
		"API_PORT":     ref(referencedService, varref.Port),
		"API_PUBLIC":   ref(referencedService, varref.PublicURL),
		dbHostName:     ref(referencedService, dbHostName),
		databaseURL:    "postgres://u:" + ref(varref.Secrets, passwordName) + "@" + ref(referencedService, dbHostName),
		"API_ENDPOINT": ref(referencedService, varref.PrivateHost) + "/v1",
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	wantPlain := map[string]string{
		buildModeKey:   releaseMode,
		apiURLName:     "http://api:9090",
		"API_PORT":     "9090",
		"API_PUBLIC":   "https://" + referencedDomain,
		dbHostName:     dbHostValue,
		"API_ENDPOINT": "api/v1",
	}
	wantSecret := map[string]string{
		databaseURL: "postgres://u:" + ref(varref.Secrets, passwordName) + "@" + dbHostValue,
	}
	deployment, err := f.queries.GetDeploymentByID(t.Context(), uuid.MustParse(resp.GetDeploymentId()))
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(deployment.PlainEnv, wantPlain) || !maps.Equal(deployment.SecretEnv, wantSecret) {
		t.Fatalf("deployment env = %v and %v, want %v and %v",
			deployment.PlainEnv, deployment.SecretEnv, wantPlain, wantSecret)
	}
	if got := desiredPayload(t, f).AppSpec.ServiceSpec.Deployment.Env; !maps.Equal(got, wantPlain) {
		t.Fatalf("application env = %v, want %v", got, wantPlain)
	}
	if got := f.placement(t, f.clusterID).SecretEnv; !maps.Equal(got, wantSecret) {
		t.Fatalf("placement secret env = %v, want %v", got, wantSecret)
	}

	server := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, testAgentConfig())
	var data map[string][]byte
	for _, msg := range f.sendPending(t, server) {
		if msg.GetApply().GetResourceId() == f.resourceID.String() {
			data = msg.GetApply().GetEnvSecret().GetData()
		}
	}
	if len(data) != 1 || string(data[databaseURL]) != "postgres://u:"+plaintextValue+"@"+dbHostValue {
		t.Fatalf("env secret data = %v, want %s rendered with the password", data, databaseURL)
	}
}

func TestSecretValuesAreStoredOnlyAsCiphertext(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	f.setSecrets(t, provider, map[string]string{passwordName: plaintextValue})
	service := imageService()
	service.Env = map[string]string{
		buildModeKey: releaseMode,
		databaseURL:  "postgres://u:" + ref(varref.Secrets, passwordName) + "@db",
	}
	resp, err := createDeploymentReadingSecrets(t, f, sameRegionSpec, service)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	var ciphertext []byte
	var row string
	if err = f.pool.QueryRow(t.Context(), `SELECT s.ciphertext, s::text FROM secrets s WHERE name = $1`, passwordName).
		Scan(&ciphertext, &row); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), plaintextValue) || strings.Contains(row, plaintextValue) ||
		strings.Contains(row, hex.EncodeToString([]byte(plaintextValue))) {
		t.Fatal("the secrets row carries the plaintext")
	}
	for _, query := range []string{
		`SELECT d::text FROM deployments d WHERE resource_id = $1`,
		`SELECT p::text FROM placements p WHERE resource_id = $1`,
	} {
		var stored string
		if err = f.pool.QueryRow(t.Context(), query, f.resourceID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stored, plaintextValue) {
			t.Fatalf("%s carries the plaintext", query)
		}
	}
	deployment, err := f.queries.GetDeploymentByID(t.Context(), uuid.MustParse(resp.GetDeploymentId()))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{buildModeKey: releaseMode}; !maps.Equal(deployment.PlainEnv, want) {
		t.Fatalf("plain env = %v, want %v", deployment.PlainEnv, want)
	}
	if want := map[string]string{databaseURL: service.GetEnv()[databaseURL]}; !maps.Equal(deployment.SecretEnv, want) {
		t.Fatalf("secret env = %v, want the template %v", deployment.SecretEnv, want)
	}
}

func TestCreateDeploymentFailsOnAReferenceThatDoesNotResolve(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		apiEnv map[string]string
		value  string
		want   error
	}{
		{"missing secret", nil, ref(varref.Secrets, missingName), varref.ErrUnknownSecret},
		{"missing resource", nil, ref("nope", varref.Port), varref.ErrUnknownResource},
		{"missing variable", nil, ref(referencedService, missingName), varref.ErrUnknownOutput},
		{"cycle", map[string]string{"A": ref(deployFixtureService, "B")}, ref(referencedService, "A"), varref.ErrCycle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newDeployFixture(t)
			api := f.referencedResource(t, tc.apiEnv, plainResolution(map[string]string{}))
			before := f.rolloutSnapshot(t)
			_, err := f.deployVariables(t, api, map[string]string{"B": tc.value})
			if !errors.Is(err, tc.want) {
				t.Fatalf("deploy: %v, want %v", err, tc.want)
			}
			if f.rolloutSnapshot(t) != before {
				t.Fatal("the refused deployment changed persisted state")
			}
		})
	}
}

func TestCreateDeploymentRequiresReadOnTheReferencedResource(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.referencedResource(t, nil, plainResolution(map[string]string{}))
	service := imageService()
	service.Env = map[string]string{apiURLName: ref(referencedService, varref.PrivateURL)}
	_, err := createDeploymentWith(t, f, sameRegionSpec, service)
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestCreateDeploymentReadingASecretRequiresListingSecrets(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	f.setSecrets(t, provider, map[string]string{passwordName: plaintextValue})
	service := imageService()
	service.Env = map[string]string{databaseURL: ref(varref.Secrets, passwordName)}
	_, err := createDeploymentWith(t, f, sameRegionSpec, service)
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestReplayReadingASecretRequiresListingSecrets(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	f.setSecrets(t, provider, map[string]string{passwordName: plaintextValue})
	seedDeployment(t, f, sameRegionSpec)
	service := imageService()
	service.Env = map[string]string{databaseURL: ref(varref.Secrets, passwordName)}
	read := genDb.EntityScope{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID(t), Scope: genDb.ScopeRead}
	initial := &deploymentv1.CreateDeploymentRequest{
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.envID.String(),
		Regions:       []string{testRegion},
		Spec:          &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
	}
	created, err := deployRequest(t, f, initial, read)
	if err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	request := &deploymentv1.CreateDeploymentRequest{
		ResourceId:         f.resourceID.String(),
		EnvironmentId:      f.envID.String(),
		Regions:            []string{testRegion},
		Trigger:            deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_REDEPLOY,
		SourceDeploymentId: created.GetDeploymentId(),
	}
	before := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`)
	if _, err = deployRequest(t, f, request); codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("replay without secret read permission = %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`); got != before {
		t.Fatal("denied replay created a deployment")
	}
	replayed, err := deployRequest(t, f, request, read)
	if err != nil {
		t.Fatal(err)
	}
	original := f.getDeployment(t, created.GetDeploymentId())
	if got := f.getDeployment(t, replayed.GetDeploymentId()); !proto.Equal(got.GetSpec(), original.GetSpec()) {
		t.Fatal("authorized replay changed the source snapshot")
	}
}

func TestReplayKeepsSecretAuthorizationAfterReferencedVariableBecomesPlain(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	f.setSecrets(t, provider, map[string]string{passwordName: plaintextValue})
	secretReference := ref(varref.Secrets, passwordName)
	api := f.referencedResource(t, map[string]string{dbHostName: secretReference}, varref.Resolved{
		Plain:  map[string]string{},
		Secret: map[string]string{dbHostName: secretReference},
	})
	created, err := f.deployVariables(t, api, map[string]string{databaseURL: ref(referencedService, dbHostName)})
	if err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	original, err := f.queries.GetDeploymentByID(t.Context(), uuid.MustParse(created.GetDeploymentId()))
	if err != nil || original.SecretEnv[databaseURL] != secretReference {
		t.Fatalf("original secret binding = %v (%v)", original.SecretEnv, err)
	}
	if _, err = f.queries.UpdateResourceEnvironmentSpec(t.Context(), genDb.UpdateResourceEnvironmentSpecParams{
		ResourceID: api, EnvironmentID: f.envID, Spec: []byte(sameRegionSpec),
	}); err != nil {
		t.Fatal(err)
	}
	plain := imageService()
	plain.Env = map[string]string{dbHostName: dbHostValue}
	if _, err = deployRequest(t, f, &deploymentv1.CreateDeploymentRequest{
		ResourceId: api.String(), EnvironmentId: f.envID.String(), Regions: []string{testRegion},
		Spec: &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: plain}},
	}, genDb.EntityScope{EntityType: genDb.EntityTypeResource, EntityID: api, Scope: genDb.ScopeWrite}); err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	request := &deploymentv1.CreateDeploymentRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.envID.String(), Regions: []string{testRegion},
		Trigger: deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_REDEPLOY, SourceDeploymentId: original.ID.String(),
	}
	readAPI := genDb.EntityScope{EntityType: genDb.EntityTypeResource, EntityID: api, Scope: genDb.ScopeRead}
	before := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`)
	if _, err = deployRequest(t, f, request, readAPI); codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("replay of captured secret after dependency became plain without secret permission = %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`); got != before {
		t.Fatal("denied replay created a deployment")
	}
	// The current dependency can no longer resolve; the replay still uses its captured bindings.
	if _, err = f.pool.Exec(t.Context(), `UPDATE deployments
		SET spec = jsonb_set(spec, '{env}', $2::jsonb) WHERE resource_id = $1`,
		api, `{"DB_HOST":"${{secrets.MISSING}}"}`); err != nil {
		t.Fatal(err)
	}

	authorized := append(f.workspaceReadScopes(t), readAPI)
	replayed, err := deployRequest(t, f, request, authorized...)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.queries.GetDeploymentByID(t.Context(), uuid.MustParse(replayed.GetDeploymentId()))
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(stored.SecretEnv, original.SecretEnv) || !maps.Equal(stored.PlainEnv, original.PlainEnv) {
		t.Fatalf("authorized replay changed captured bindings: %v", stored)
	}
	f.storeSecret(t, provider, passwordName, strings.Repeat("s", testMaxPlacementBytes))
	if _, err = deployRequest(t, f, request, authorized...); !errors.Is(err, ErrSecretEnvTooLarge) {
		t.Fatalf("replay with oversized captured secret binding = %v", err)
	}
	if _, err = f.pool.Exec(t.Context(), `DELETE FROM secrets WHERE environment_id = $1 AND name = $2`,
		f.envID, passwordName); err != nil {
		t.Fatal(err)
	}
	if _, err = deployRequest(t, f, request, authorized...); !errors.Is(err, varref.ErrUnknownSecret) {
		t.Fatalf("replay with missing captured secret binding = %v", err)
	}
}

func TestDeleteSecretsRefusesANameAResourceReads(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	f.setSecrets(t, provider, map[string]string{passwordName: plaintextValue, unusedKey: unusedValue})
	api := f.referencedResource(t, nil, plainResolution(map[string]string{}))
	env := map[string]string{databaseURL: ref(varref.Secrets, passwordName)}
	if _, err := f.deployVariables(t, api, env); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	server := f.secretServer(provider)
	ctx := f.secretWriterContext(t)
	_, err := server.DeleteSecrets(ctx, &secretv1.DeleteSecretsRequest{
		EnvironmentId: f.envID.String(), Names: []string{passwordName, unusedKey},
	})
	code := wireCode(err)
	if code != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), deployFixtureService) {
		t.Fatalf("delete of a read secret: %v, want FailedPrecondition naming %s", err, deployFixtureService)
	}
	listed, err := server.ListSecrets(ctx, &secretv1.ListSecretsRequest{EnvironmentId: f.envID.String()})
	if err != nil || len(listed.GetSecrets()) != 2 {
		t.Fatalf("secrets after the refused delete = %v (%v), want both", listed.GetSecrets(), err)
	}
}

func TestListSecretsNamesTheResourcesThatReadEachSecret(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	f.setSecrets(t, provider, map[string]string{passwordName: plaintextValue, unusedKey: unusedValue})
	template := "postgres://u:" + ref(varref.Secrets, passwordName) + "@db"
	api := f.referencedResource(t, map[string]string{"DB_URL": template}, varref.Resolved{
		Plain: map[string]string{}, Secret: map[string]string{"DB_URL": template},
	})
	if _, err := f.deployVariables(
		t,
		api,
		map[string]string{databaseURL: ref(referencedService, "DB_URL")},
	); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	server := f.secretServer(provider)
	ctx := f.secretWriterContext(t)
	listed, err := server.ListSecrets(ctx, &secretv1.ListSecretsRequest{EnvironmentId: f.envID.String()})
	if err != nil {
		t.Fatal(err)
	}
	users := map[string][]string{}
	for _, secret := range listed.GetSecrets() {
		users[secret.GetName()] = []string{}
		for _, user := range secret.GetUsedBy() {
			users[secret.GetName()] = append(users[secret.GetName()], user.GetResourceSlug())
		}
	}
	if got := users[passwordName]; len(got) != 2 || got[0] != referencedService || got[1] != deployFixtureService {
		t.Fatalf("%s used by %v, want %s and %s", passwordName, got, referencedService, deployFixtureService)
	}
	if got, found := users[unusedKey]; !found || len(got) != 0 {
		t.Fatalf("%s used by %v, want listed and unread", unusedKey, got)
	}

	_, err = server.DeleteSecrets(ctx, &secretv1.DeleteSecretsRequest{
		EnvironmentId: f.envID.String(), Names: []string{passwordName},
	})
	if !errors.Is(err, ErrSecretInUse) || !strings.Contains(err.Error(), referencedService+", "+deployFixtureService) {
		t.Fatalf("delete a secret two resources read: %v, want %v naming both", err, ErrSecretInUse)
	}
	if _, err = server.DeleteSecrets(ctx, &secretv1.DeleteSecretsRequest{
		EnvironmentId: f.envID.String(), Names: []string{unusedKey},
	}); err != nil {
		t.Fatalf("delete a secret nothing reads: %v", err)
	}
	if _, err = f.pool.Exec(ctx, `DELETE FROM secrets WHERE environment_id = $1 AND name = $2`,
		f.envID, passwordName); err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	_, err = f.deployVariables(t, api, map[string]string{databaseURL: ref(varref.Secrets, passwordName)})
	if !errors.Is(err, varref.ErrUnknownSecret) {
		t.Fatalf("deploy after the delete: %v, want %v", err, varref.ErrUnknownSecret)
	}
}

func TestListResourceOutputsListsBuiltInsVariablesAndUsers(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	apiEnv := map[string]string{dbHostName: dbHostValue, "BROKEN": ref("nope", varref.Port)}
	api := f.referencedResource(t, apiEnv, plainResolution(apiEnv))
	if _, err := f.deployVariables(
		t,
		api,
		map[string]string{apiURLName: ref(referencedService, varref.PrivateURL)},
	); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: f.workspaceReadScopes(t)})
	resp, err := server.ListResourceOutputs(
		ctx,
		&resourcev1.ListResourceOutputsRequest{EnvironmentId: f.envID.String()},
	)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*resourcev1.ResourceOutputs{}
	for _, resource := range resp.GetResources() {
		byName[resource.GetResourceSlug()] = resource
	}
	outputs := map[string]string{}
	builtIn := map[string]bool{}
	for _, output := range byName[referencedService].GetOutputs() {
		outputs[output.GetKey()] = output.GetValue()
		builtIn[output.GetKey()] = output.GetBuiltIn()
	}
	want := map[string]string{
		varref.PrivateHost: referencedService,
		varref.Port:        "9090",
		varref.PrivateURL:  "http://api:9090",
		varref.PublicURL:   "https://" + referencedDomain,
		dbHostName:         dbHostValue,
	}
	if !maps.Equal(outputs, want) || !builtIn[varref.PrivateURL] || builtIn[dbHostName] {
		t.Fatalf("outputs of %s = %v (built in %v), want %v", referencedService, outputs, builtIn, want)
	}
	if used := byName[referencedService].GetUsedBy(); len(used) != 1 || used[0] != deployFixtureService {
		t.Fatalf("%s used by %v, want %s", referencedService, used, deployFixtureService)
	}
	if used := byName[deployFixtureService].GetUsedBy(); len(used) != 0 {
		t.Fatalf("%s used by %v, want none", deployFixtureService, used)
	}
}

func TestRegionUpdateKeepsTheResolvedVariablesOfTheRunningDeployment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.setSecrets(t, newTestKeyProvider(t, "k1"), map[string]string{passwordName: plaintextValue})
	service := imageService()
	service.Env = map[string]string{buildModeKey: releaseMode, "AUTH_TOKEN": ref(varref.Secrets, passwordName)}
	resp, err := createDeploymentReadingSecrets(t, f, sameRegionSpec, service)
	if err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	running, err := f.queries.GetDeploymentByID(t.Context(), uuid.MustParse(resp.GetDeploymentId()))
	if err != nil {
		t.Fatal(err)
	}

	targets, revision := regionTargets(t, f)
	targets[testRegion].Cpu = "200m"
	if err = updateResourceRegions(t, f, targets, revision); err != nil {
		t.Fatal(err)
	}
	updated, err := f.queries.GetDeploymentByID(t.Context(), f.liveDeployment(t))
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID == running.ID || !maps.Equal(updated.PlainEnv, running.PlainEnv) ||
		!maps.Equal(updated.SecretEnv, running.SecretEnv) {
		t.Fatalf("region update = %s with %v and %v, want a new deployment with %v and %v",
			updated.ID, updated.PlainEnv, updated.SecretEnv, running.PlainEnv, running.SecretEnv)
	}
	if got := desiredPayload(t, f).AppSpec.ServiceSpec.Deployment.Env; !maps.Equal(got, running.PlainEnv) {
		t.Fatalf("application env after the region update = %v, want %v", got, running.PlainEnv)
	}
}
