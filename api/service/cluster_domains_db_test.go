package service

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	domainsSecondCluster  = "eu-cluster"
	domainsOtherPlatform  = "example.org"
	domainsServedHostname = "app.example.com"
	domainsFirst          = "a.test"
	domainsSecond         = "b.test"
	domainsThird          = "c.test"
	domainsPath           = "domains"
	domainsTwoRegionSpec  = `{"routing":{"port":8080},"regions":{` +
		`"us-east-1":{"enabled":true,"cpu":"100m","memory":"64Mi","minReplicas":1,"maxReplicas":1},` +
		`"eu-west-1":{"enabled":true,"cpu":"100m","memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`
	domainsTwoRegionFile = `  new:
    port: 8080
    domains: [new.loco.test]
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
      eu-west-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
)

func (f *deployFixture) clusterDomains(t *testing.T, clusterID uuid.UUID) []string {
	t.Helper()
	var domains []string
	query := `SELECT domains FROM clusters WHERE id = $1`
	if err := f.pool.QueryRow(context.Background(), query, clusterID).Scan(&domains); err != nil {
		t.Fatalf("cluster domains: %v", err)
	}
	return domains
}

func (f *deployFixture) setClusterDomains(t *testing.T, clusterID uuid.UUID, domains ...string) {
	t.Helper()
	update := `UPDATE clusters SET domains = $2 WHERE id = $1`
	if _, err := f.pool.Exec(context.Background(), update, clusterID, append([]string{}, domains...)); err != nil {
		t.Fatalf("set cluster domains: %v", err)
	}
}

func (f *deployFixture) addRegionCluster(t *testing.T, domains ...string) {
	t.Helper()
	insert := `
INSERT INTO clusters (name, region, provider, is_active, is_default, health_status, last_heartbeat, domains)
VALUES ($1, $2, 'kind', true, true, 'healthy', NOW(), $3)`
	served := append([]string{}, domains...)
	if _, err := f.pool.Exec(
		context.Background(),
		insert,
		domainsSecondCluster,
		statusSecondRegion,
		served,
	); err != nil {
		t.Fatalf("add region cluster: %v", err)
	}
}

func TestRegisterAndHeartbeatRecordTheReportedDomains(t *testing.T) {
	e := newEnrollmentFixture(t)
	clusterID, token := e.createCluster(t)
	key := newAgentKey(t)
	clusterKey := uuid.New()
	_, err := e.agent.Register(t.Context(), &agentv1.RegisterRequest{
		ClusterKey: clusterKey.String(),
		Enrollment: &agentv1.Enrollment{JoinToken: token, PublicKey: publicKeyBytes(t, key)},
		Domains:    []string{domainsSecond, domainsFirst, domainsFirst},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := e.clusterDomains(t, clusterID); !slices.Equal(got, []string{domainsFirst, domainsSecond}) {
		t.Fatalf("domains after register = %v, want [a.test b.test]", got)
	}

	server := e.agentServer()
	server.recordHeartbeat(t.Context(), clusterID, &agentv1.Heartbeat{Domains: []string{domainsThird}})
	if got := e.clusterDomains(t, clusterID); !slices.Equal(got, []string{domainsThird}) {
		t.Fatalf("domains after heartbeat = %v, want [c.test]", got)
	}
	server.recordHeartbeat(t.Context(), clusterID, &agentv1.Heartbeat{})
	if got := e.clusterDomains(t, clusterID); len(got) != 0 {
		t.Fatalf("domains after an empty heartbeat = %v, want none", got)
	}
}

func TestRegisterRejectsAnInvalidDomain(t *testing.T) {
	e := newEnrollmentFixture(t)
	_, token := e.createCluster(t)
	key := newAgentKey(t)
	_, err := e.agent.Register(t.Context(), &agentv1.RegisterRequest{
		ClusterKey: uuid.NewString(),
		Enrollment: &agentv1.Enrollment{JoinToken: token, PublicKey: publicKeyBytes(t, key)},
		Domains:    []string{"*.example.com"},
	})
	requireCode(t, err, connect.CodeInvalidArgument, "register with a wildcard domain")
}

func TestPlanRejectsADomainOneRegionsClusterDoesNotServe(t *testing.T) {
	f := newDeployFixture(t)
	f.serveDomains(t, testPlatformDomain)
	f.addRegionCluster(t, domainsOtherPlatform)

	resp, err := plan(t, f, planFileHeader+domainsTwoRegionFile, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(resp.GetOperations()) != 0 || len(resp.GetErrors()) != 1 {
		t.Fatalf("plan = %v, want one error and no operations", resp)
	}
	want := "a cluster the service deploys to does not serve the domain: new.loco.test is under loco.test, " +
		"which eu-cluster (eu-west-1) does not serve"
	planErr := resp.GetErrors()[0]
	if planErr.GetPath() != domainsPath || planErr.GetMessage() != want {
		t.Fatalf("error = %v, want %q", planErr, want)
	}

	singleRegion := strings.Replace(domainsTwoRegionFile,
		"      eu-west-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }\n", "", 1)
	resp, err = plan(t, f, planFileHeader+singleRegion, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(resp.GetErrors()) != 0 {
		t.Fatalf("errors = %v, want none for the region whose cluster serves the domain", resp.GetErrors())
	}
}

func TestPlanRejectsADomainNoClusterServes(t *testing.T) {
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	file := planFileHeader + strings.Replace(planFileNew, "routing: {}", "domains: [new.loco.test]", 1)

	resp, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := "domain is not under a platform domain any cluster serves: new.loco.test"
	if len(resp.GetErrors()) != 1 || resp.GetErrors()[0].GetMessage() != want {
		t.Fatalf("errors = %v, want %q", resp.GetErrors(), want)
	}
}

func TestCreateResourceDomainNamesTheClustersMissingTheDomain(t *testing.T) {
	f := newDeployFixture(t)
	client := newDomainClient(t, f)
	f.addRegionCluster(t, domainsOtherPlatform)
	seedDeployment(t, f, domainsTwoRegionSpec)

	err := client.add(domainsServedHostname)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), "which eu-cluster (eu-west-1) does not serve") {
		t.Fatalf("err = %v, want it to name eu-cluster", err)
	}
	if n := f.count(t, `SELECT count(*) FROM resource_domains WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d domains after a rejected add, want 0", n)
	}
}

func TestCreateResourceDomainNeedsTheDomainOnTheClusterThatRunsIt(t *testing.T) {
	f := newDeployFixture(t)
	client := newDomainClient(t, f)
	if _, err := f.deployTo(context.Background(), f.otherCluster, staticSpec); err != nil {
		t.Fatalf("deploy to c2: %v", err)
	}
	f.setClusterDomains(t, f.otherCluster)

	err := client.add(domainsServedHostname)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), "which c2 (us-east-1) does not serve") {
		t.Fatalf("err = %v, want it to name c2, the cluster the resource runs on", err)
	}
}

func TestPlatformSubdomainIsUniquePerDomain(t *testing.T) {
	f := newDeployFixture(t)
	client := newDomainClient(t, f)
	f.serveDomains(t, testDomainPlatform, domainsOtherPlatform)

	if err := client.add("api." + testDomainPlatform); err != nil {
		t.Fatalf("add api.%s: %v", testDomainPlatform, err)
	}
	if err := client.addUnder("api", domainsOtherPlatform); err != nil {
		t.Fatalf("add the same label under %s: %v", domainsOtherPlatform, err)
	}
	wantCode(t, client.add("api."+testDomainPlatform), connect.CodeAlreadyExists)

	duplicate := `
INSERT INTO resource_domains
    (resource_id, environment_id, domain, domain_source, subdomain_label, platform_domain, is_primary)
VALUES ($1, $2, 'api-copy.example.com', 'platform_provided', 'api', 'example.com', false)`
	_, err := f.pool.Exec(context.Background(), duplicate, f.resourceID, f.envID)
	if !isPgConstraintViolation(err) {
		t.Fatalf("insert a second api label under example.com: err = %v, want a unique violation", err)
	}
}

func TestListPlatformDomainsListsTheRegionsWhoseClusterServesEachDomain(t *testing.T) {
	f := newDeployFixture(t)
	client := newDomainClient(t, f)
	f.setClusterDomains(t, f.clusterID, domainsFirst, domainsSecond)
	f.setClusterDomains(t, f.otherCluster, "shadowed.test")
	f.addRegionCluster(t, domainsSecond)
	staging := f.addStagingEnvironment(t)

	scopes := f.workspaceReadScopes(t)
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	resp, err := client.server.ListPlatformDomains(ctx, &domainv1.ListPlatformDomainsRequest{
		EnvironmentId: f.envID.String(),
	})
	if err != nil {
		t.Fatalf("ListPlatformDomains: %v", err)
	}
	got := map[string][]string{}
	for _, domain := range resp.GetPlatformDomains() {
		got[domain.GetDomain()] = domain.GetRegions()
	}
	want := map[string][]string{domainsFirst: {testRegion}, domainsSecond: {statusSecondRegion, testRegion}}
	if len(got) != len(want) {
		t.Fatalf("domains = %v, want %v", got, want)
	}
	for domain, regions := range want {
		if !slices.Equal(got[domain], regions) {
			t.Fatalf("domains = %v, want %v", got, want)
		}
	}

	resp, err = client.server.ListPlatformDomains(ctx, &domainv1.ListPlatformDomainsRequest{
		EnvironmentId: staging.String(),
	})
	if err != nil {
		t.Fatalf("ListPlatformDomains staging: %v", err)
	}
	if len(resp.GetPlatformDomains()) != 0 {
		t.Fatalf("staging domains = %v, want none without a staging cluster", resp.GetPlatformDomains())
	}

	resourceScopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeAdmin},
	}
	denied := context.WithValue(context.Background(), contextkeys.EntityScopesKey, resourceScopes)
	_, err = client.server.ListPlatformDomains(denied, &domainv1.ListPlatformDomainsRequest{
		EnvironmentId: f.envID.String(),
	})
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestAClusterThatStopsServingADomainKeepsTheHostnameAndDegradesTheRegion(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.setClusterDomains(t, f.clusterID, testDomainPlatform)
	insert := `
INSERT INTO resource_domains
    (resource_id, environment_id, domain, domain_source, subdomain_label, platform_domain, is_primary)
VALUES ($1, $2, $3, 'platform_provided', 'app', $4, true)`
	_, err := f.pool.Exec(
		context.Background(),
		insert,
		f.resourceID,
		f.envID,
		domainsServedHostname,
		testDomainPlatform,
	)
	if err != nil {
		t.Fatalf("assign hostname: %v", err)
	}
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
	})

	f.agentServer().recordHeartbeat(t.Context(), f.clusterID, &agentv1.Heartbeat{
		Health: &agentv1.AgentHealth{KubernetesHealthy: true, ControllerHealthy: true},
	})
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
		ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
		message: regionMessageDomainsUnserved + domainsServedHostname,
	})
	if n := f.count(t, `SELECT count(*) FROM resource_domains WHERE resource_id = $1`); n != 1 {
		t.Fatalf("%d domains after the cluster dropped the domain, want the assignment kept", n)
	}

	f.agentServer().recordHeartbeat(t.Context(), f.clusterID, &agentv1.Heartbeat{
		Health:  &agentv1.AgentHealth{KubernetesHealthy: true, ControllerHealthy: true},
		Domains: []string{testDomainPlatform},
	})
	f.heartbeat(t, f.clusterID, time.Now(), clusterHealthHealthy)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
	})
}

func (c *domainClient) addUnder(label, platformDomain string) error {
	input := &domainv1.DomainInput{
		DomainSource:   domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED,
		Subdomain:      &label,
		PlatformDomain: &platformDomain,
	}
	resourceID := c.f.resourceID.String()
	req := &domainv1.CreateResourceDomainRequest{ResourceId: resourceID, Domain: input}
	_, err := c.server.CreateResourceDomain(c.ctx, req)
	return err
}
