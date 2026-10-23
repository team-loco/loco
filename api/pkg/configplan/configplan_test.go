package configplan

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/internal/locofile"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testPartial        = webService
	testRegion         = "us-east-1"
	testImage          = "ghcr.io/acme/worker:1.4.2"
	webService         = "web"
	workerService      = "worker"
	apiService         = "api"
	oldService         = "old"
	otherPartial       = "backend"
	sessionSecret      = "SESSION_KEY"
	logLevelKey        = "LOG_LEVEL"
	logLevelPath       = "env." + logLevelKey
	testRegionPath     = "regions." + testRegion
	logLevelInfo       = "info"
	testCPU            = "250m"
	testMemory         = "512Mi"
	appDomain          = "app.example.com"
	testPlatformDomain = "example.com"
	testPinned         = "ghcr.io/acme/worker@sha256:7781a08afca1adb11b6294ca81ee6e04f9fc677f4f04c9d9daf2b0e068f5e89a"
)

func testDefaults() servicedefaults.Defaults {
	return servicedefaults.Defaults{
		CPU:                 "100m",
		Memory:              "256Mi",
		MinReplicas:         1,
		MaxReplicas:         2,
		PathPrefix:          "/",
		IdleTimeout:         60,
		Port:                8000,
		HealthPath:          "/health",
		HealthInterval:      30,
		HealthTimeout:       5,
		HealthFailThreshold: 3,
	}
}

func fileService() locofile.Service {
	port := int32(3000)
	minReplicas, maxReplicas := int32(1), int32(3)
	return locofile.Service{
		Dockerfile: locofile.DefaultDockerfile,
		Port:       &port,
		Routing:    &locofile.Routing{},
		Domains:    []string{appDomain},
		Env:        map[string]string{logLevelKey: logLevelInfo},
		Regions: map[string]locofile.Region{
			testRegion: {
				CPU:      testCPU,
				Memory:   testMemory,
				Replicas: &locofile.Replicas{Min: &minReplicas, Max: &maxReplicas},
			},
		},
	}
}

func liveState() State {
	return State{
		Dockerfile: locofile.DefaultDockerfile,
		Context:    locofile.DefaultContext,
		Port:       3000,
		Routing:    &Routing{PathPrefix: "/", IdleTimeout: 60},
		Domains:    []string{appDomain},
		Env:        map[string]string{logLevelKey: logLevelInfo},
		Regions:    map[string]Region{testRegion: {CPU: testCPU, Memory: testMemory, MinReplicas: 1, MaxReplicas: 3}},
	}
}

const (
	apiKeySecret       = "API_KEY"
	testMaxSecretBytes = 1024
)

func input(services map[string]locofile.Service, live ...Service) Input {
	return Input{
		Partial:        testPartial,
		Services:       services,
		Environments:   []string{"prod"},
		Clusters:       []Cluster{testCluster(testRegion)},
		MaxSecretBytes: testMaxSecretBytes,
		Live:           live,
		Defaults:       testDefaults(),
		Images:         map[string]ImageResult{testImage: {Pinned: testPinned}},
	}
}

func testCluster(region string) Cluster {
	return Cluster{Name: "cluster-" + region, Region: region, Domains: []string{testPlatformDomain}}
}

func compute(t *testing.T, in Input) Plan {
	t.Helper()
	plan, err := Compute(in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return plan
}

func onlyOperation(t *testing.T, plan Plan) Operation {
	t.Helper()
	if len(plan.Errors) > 0 {
		t.Fatalf("errors = %v, want none", plan.Errors)
	}
	if len(plan.Operations) != 1 {
		t.Fatalf("operations = %v, want one", plan.Operations)
	}
	return plan.Operations[0]
}

func onlyError(t *testing.T, plan Plan, want error) Error {
	t.Helper()
	if len(plan.Operations) != 0 {
		t.Fatalf("operations = %v, want none alongside errors", plan.Operations)
	}
	if len(plan.Errors) != 1 {
		t.Fatalf("errors = %v, want one", plan.Errors)
	}
	if !errors.Is(plan.Errors[0].Err, want) {
		t.Fatalf("error = %v, want %v", plan.Errors[0].Err, want)
	}
	return plan.Errors[0]
}

func TestOmittedFieldsAreFilledWithDefaultsAndNotChanges(t *testing.T) {
	live := Service{
		Name:           webService,
		Partial:        testPartial,
		Built:          true,
		RunningRegions: []string{testRegion},
		State:          liveState(),
	}
	plan := compute(t, input(map[string]locofile.Service{webService: fileService()}, live))
	if len(plan.Operations) != 0 || len(plan.Errors) != 0 {
		t.Fatalf("plan = %+v, want no operations for a service that matches the file", plan)
	}
	if desired, listed := plan.Desired[webService]; !listed || !reflect.DeepEqual(desired, liveState()) {
		t.Fatalf("desired = %+v listed %v, want the unchanged service's state", desired, listed)
	}
}

func TestNewServiceIsACreateListingEveryField(t *testing.T) {
	plan := compute(t, input(map[string]locofile.Service{webService: fileService()}))
	op := onlyOperation(t, plan)
	if op.Kind != KindCreate || !op.NeedsDeploy || op.Destructive {
		t.Fatalf("operation = %+v, want a create that needs a deploy", op)
	}
	want := []Change{
		{Path: "context", After: locofile.DefaultContext},
		{Path: "dockerfile", After: locofile.DefaultDockerfile},
		{Path: "domains", After: "[app.example.com]"},
		{Path: logLevelPath, After: logLevelInfo},
		{Path: "port", After: "3000"},
		{Path: "regions.us-east-1.cpu", After: testCPU},
		{Path: "regions.us-east-1.memory", After: testMemory},
		{Path: "regions.us-east-1.replicas.max", After: "3"},
		{Path: "regions.us-east-1.replicas.min", After: "1"},
		{Path: "routing.idleTimeout", After: "60"},
		{Path: "routing.pathPrefix", After: "/"},
	}
	if !reflect.DeepEqual(want, op.Changes) {
		t.Fatalf("changes = %v, want %v", op.Changes, want)
	}
	if !reflect.DeepEqual(op.Desired, liveState()) {
		t.Fatalf("desired = %+v, want the file service with defaults filled", op.Desired)
	}
}

func TestChangedFieldsMakeAnUpdate(t *testing.T) {
	service := fileService()
	service.Env["LOG_LEVEL"] = "debug"
	service.Secrets = []string{sessionSecret}
	live := Service{
		Name:           webService,
		Partial:        testPartial,
		Built:          true,
		RunningRegions: []string{testRegion},
		State:          liveState(),
	}
	in := input(map[string]locofile.Service{webService: service}, live)
	in.Secrets = map[string]map[string]int64{webService: {sessionSecret: 1}}

	op := onlyOperation(t, compute(t, in))
	if op.Kind != KindUpdate || op.NeedsDeploy || op.Destructive {
		t.Fatalf("operation = %+v, want an update", op)
	}
	want := []Change{
		{Path: logLevelPath, Before: logLevelInfo, After: "debug"},
		{Path: "secrets", After: "[SESSION_KEY]"},
	}
	if !reflect.DeepEqual(want, op.Changes) {
		t.Fatalf("changes = %v, want %v", op.Changes, want)
	}
}

func TestRemovedRoutingAndEnvShowAsChanges(t *testing.T) {
	service := fileService()
	service.Routing = nil
	service.Env = nil
	live := Service{
		Name:           webService,
		Partial:        testPartial,
		Built:          true,
		RunningRegions: []string{testRegion},
		State:          liveState(),
	}
	op := onlyOperation(t, compute(t, input(map[string]locofile.Service{webService: service}, live)))
	want := []Change{
		{Path: logLevelPath, Before: logLevelInfo},
		{Path: "routing.idleTimeout", Before: "60"},
		{Path: "routing.pathPrefix", Before: "/"},
	}
	if !reflect.DeepEqual(want, op.Changes) {
		t.Fatalf("changes = %v, want %v", op.Changes, want)
	}
}

func TestUnownedServiceIsImported(t *testing.T) {
	live := Service{Name: webService, Built: true, RunningRegions: []string{testRegion}, State: liveState()}
	op := onlyOperation(t, compute(t, input(map[string]locofile.Service{webService: fileService()}, live)))
	if op.Kind != KindImport || len(op.Changes) != 0 || op.Destructive {
		t.Fatalf("operation = %+v, want an import without changes", op)
	}
}

func TestRemovingARunningRegionIsDestructive(t *testing.T) {
	const movedRegion = "eu-west-1"
	service := fileService()
	service.Regions = map[string]locofile.Region{movedRegion: service.Regions[testRegion]}
	for _, partial := range []string{testPartial, ""} {
		live := Service{
			Name:           webService,
			Partial:        partial,
			Built:          true,
			RunningRegions: []string{testRegion},
			State:          liveState(),
		}
		in := input(map[string]locofile.Service{webService: service}, live)
		in.Clusters = []Cluster{testCluster(testRegion), testCluster(movedRegion)}
		op := onlyOperation(t, compute(t, in))
		if !op.Destructive {
			t.Fatalf("%v operation = %+v, want destructive for the stopped region", op.Kind, op)
		}
	}
}

func TestRemovingARegionThatDoesNotRunIsNotDestructive(t *testing.T) {
	const movedRegion = "eu-west-1"
	service := fileService()
	service.Regions = map[string]locofile.Region{movedRegion: service.Regions[testRegion]}
	live := Service{Name: webService, Partial: testPartial, Built: true, State: liveState()}
	in := input(map[string]locofile.Service{webService: service}, live)
	in.Clusters = []Cluster{testCluster(testRegion), testCluster(movedRegion)}
	if op := onlyOperation(t, compute(t, in)); op.Destructive {
		t.Fatalf("operation = %+v, want no confirmation for a region nothing runs in", op)
	}
}

func TestReorderedSecondaryDomainsAreNotAChange(t *testing.T) {
	service := fileService()
	service.Domains = []string{appDomain, "z.example.com", "a.example.com"}
	state := liveState()
	state.Domains = []string{appDomain, "a.example.com", "z.example.com"}
	live := Service{
		Name:           webService,
		Partial:        testPartial,
		Built:          true,
		RunningRegions: []string{testRegion},
		State:          state,
	}
	plan := compute(t, input(map[string]locofile.Service{webService: service}, live))
	if len(plan.Operations) != 0 || len(plan.Errors) != 0 {
		t.Fatalf("plan = %+v, want no operations for a reordered secondary domain", plan)
	}
}

func TestOwnedServiceAbsentFromTheFileIsADestructiveDelete(t *testing.T) {
	live := Service{
		Name:           oldService,
		Partial:        testPartial,
		Built:          true,
		RunningRegions: []string{testRegion},
		State:          liveState(),
	}
	op := onlyOperation(t, compute(t, input(map[string]locofile.Service{}, live)))
	if op.Kind != KindDelete || !op.Destructive || op.Service != oldService {
		t.Fatalf("operation = %+v, want a destructive delete of old", op)
	}
}

func TestAnotherPartialsServiceIsIgnoredWhenAbsentFromTheFile(t *testing.T) {
	live := Service{
		Name:           apiService,
		Partial:        otherPartial,
		Built:          true,
		RunningRegions: []string{testRegion},
		State:          liveState(),
	}
	plan := compute(t, input(map[string]locofile.Service{}, live))
	if len(plan.Operations) != 0 || len(plan.Errors) != 0 {
		t.Fatalf("plan = %+v, want nothing", plan)
	}
}

func TestAnotherPartialsServiceNamedInTheFileIsAnError(t *testing.T) {
	live := Service{
		Name:           webService,
		Partial:        otherPartial,
		Built:          true,
		RunningRegions: []string{testRegion},
		State:          liveState(),
	}
	plan := compute(t, input(map[string]locofile.Service{webService: fileService()}, live))
	planErr := onlyError(t, plan, ErrOwnedByOtherPartial)
	if planErr.Service != webService {
		t.Fatalf("error service = %q, want web", planErr.Service)
	}
}

func TestSourceServiceWithoutABuildNeedsADeploy(t *testing.T) {
	live := Service{Name: webService, Partial: testPartial, RunningRegions: []string{testRegion}, State: liveState()}
	op := onlyOperation(t, compute(t, input(map[string]locofile.Service{webService: fileService()}, live)))
	if op.Kind != KindUpdate || len(op.Changes) != 0 || !op.NeedsDeploy {
		t.Fatalf("operation = %+v, want an update with no changes that needs a deploy", op)
	}
}

func TestChangedBuildInputsNeedADeploy(t *testing.T) {
	service := fileService()
	service.Dockerfile = "build/Dockerfile"
	service.Context = "services/web"
	live := Service{
		Name:           webService,
		Partial:        testPartial,
		Built:          true,
		RunningRegions: []string{testRegion},
		State:          liveState(),
	}
	op := onlyOperation(t, compute(t, input(map[string]locofile.Service{webService: service}, live)))
	if op.Kind != KindUpdate || !op.NeedsDeploy {
		t.Fatalf("operation = %+v, want an update that needs a deploy", op)
	}
	want := []Change{
		{Path: "context", Before: locofile.DefaultContext, After: "services/web"},
		{Path: "dockerfile", Before: locofile.DefaultDockerfile, After: "build/Dockerfile"},
	}
	if !reflect.DeepEqual(want, op.Changes) {
		t.Fatalf("changes = %v, want %v", op.Changes, want)
	}
}

func TestImageServiceIsPinnedToTheDigestAndNeedsNoDeploy(t *testing.T) {
	service := fileService()
	service.Dockerfile = ""
	service.Image = testImage
	op := onlyOperation(t, compute(t, input(map[string]locofile.Service{workerService: service})))
	if op.Kind != KindCreate || op.NeedsDeploy {
		t.Fatalf("operation = %+v, want a create that needs no deploy", op)
	}
	want := Change{Path: "image", After: testPinned}
	if !slices.Contains(op.Changes, want) {
		t.Fatalf("changes = %v, want %v", op.Changes, want)
	}
}

func TestUnresolvedImageIsAnError(t *testing.T) {
	service := fileService()
	service.Dockerfile = ""
	service.Image = testImage
	in := input(map[string]locofile.Service{workerService: service})
	in.Images[testImage] = ImageResult{Err: errors.New("manifest unknown")}
	planErr := onlyError(t, compute(t, in), ErrImageUnresolved)
	if planErr.Service != workerService || planErr.Path != "image" {
		t.Fatalf("error = %+v, want worker image", planErr)
	}
}

func TestImageWithoutAResolutionBreaksTheContract(t *testing.T) {
	service := fileService()
	service.Dockerfile = ""
	service.Image = "ghcr.io/acme/other:1"
	_, err := Compute(input(map[string]locofile.Service{workerService: service}))
	if !errors.Is(err, ErrImageNotResolved) {
		t.Fatalf("Compute = %v, want %v", err, ErrImageNotResolved)
	}
}

func TestUnknownRegionIsAnError(t *testing.T) {
	service := fileService()
	service.Regions["mars-1"] = service.Regions[testRegion]
	planErr := onlyError(t, compute(t, input(map[string]locofile.Service{webService: service})), ErrUnknownRegion)
	if planErr.Path != "regions.mars-1" {
		t.Fatalf("error path = %q, want regions.mars-1", planErr.Path)
	}
}

func TestInvalidDesiredValuesAreFieldErrors(t *testing.T) {
	cases := map[string]struct {
		change func(*locofile.Service)
		path   string
	}{
		"unparsable cpu": {func(s *locofile.Service) {
			region := s.Regions[testRegion]
			region.CPU = "invalid"
			s.Regions[testRegion] = region
		}, testRegionPath},
		"memory above the limit": {func(s *locofile.Service) {
			region := s.Regions[testRegion]
			region.Memory = "64Gi"
			s.Regions[testRegion] = region
		}, testRegionPath},
		"too many replicas": {func(s *locofile.Service) {
			high := int32(11)
			s.Regions[testRegion].Replicas.Max = &high
		}, testRegionPath},
		"autoscaling target above 100": {func(s *locofile.Service) {
			region := s.Regions[testRegion]
			target := int32(150)
			region.Autoscaling = &locofile.Autoscaling{CPUTarget: &target}
			s.Regions[testRegion] = region
		}, testRegionPath},
		"privileged port": {func(s *locofile.Service) {
			port := int32(80)
			s.Port = &port
		}, "port"},
		"health timeout above the limit": {func(s *locofile.Service) {
			timeout := int32(61)
			s.Health = &locofile.Health{Timeout: &timeout}
		}, "health"},
		"relative path prefix": {func(s *locofile.Service) {
			s.Routing = &locofile.Routing{PathPrefix: "relative"}
		}, "routing"},
		"empty env value": {func(s *locofile.Service) {
			s.Env = map[string]string{logLevelKey: ""}
		}, "env"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			service := fileService()
			tc.change(&service)
			plan := compute(t, input(map[string]locofile.Service{webService: service}))
			if len(plan.Operations) != 0 {
				t.Fatalf("operations = %v, want none", plan.Operations)
			}
			if len(plan.Errors) != 1 {
				t.Fatalf("errors = %v, want one", plan.Errors)
			}
			planErr := plan.Errors[0]
			if planErr.Service != webService || planErr.Path != tc.path {
				t.Fatalf("error = %+v, want %s at %s", planErr, webService, tc.path)
			}
		})
	}
}

func TestCustomDomainIsAnError(t *testing.T) {
	service := fileService()
	service.Domains = []string{appDomain, "www.acme.dev"}
	planErr := onlyError(t, compute(t, input(map[string]locofile.Service{webService: service})), ErrCustomDomain)
	wantMessage := "domain is not under a platform domain any cluster serves: www.acme.dev"
	if planErr.Path != "domains" || planErr.Err.Error() != wantMessage {
		t.Fatalf("error = %+v, want the custom domain", planErr)
	}
}

func TestDomainMustBeServedByEveryClusterTheServiceDeploysTo(t *testing.T) {
	const otherRegion = "eu-west-1"
	service := fileService()
	service.Regions = map[string]locofile.Region{
		testRegion:  service.Regions[testRegion],
		otherRegion: service.Regions[testRegion],
	}
	missing := Cluster{Name: "cluster-eu", Region: otherRegion, Domains: []string{"other.test"}}
	in := input(map[string]locofile.Service{webService: service})
	in.Clusters = []Cluster{testCluster(testRegion), missing}
	planErr := onlyError(t, compute(t, in), ErrDomainNotServed)
	wantMessage := "a cluster the service deploys to does not serve the domain: " +
		appDomain + " is under " + testPlatformDomain + ", which cluster-eu (eu-west-1) does not serve"
	if planErr.Path != pathDomains || planErr.Err.Error() != wantMessage {
		t.Fatalf("error = %+v, want %q", planErr, wantMessage)
	}
}

func TestDomainServedOnlyInARegionTheServiceSkipsIsAllowed(t *testing.T) {
	missing := Cluster{Name: "cluster-eu", Region: "eu-west-1"}
	in := input(map[string]locofile.Service{webService: fileService()})
	in.Clusters = append(in.Clusters, missing)
	if plan := compute(t, in); len(plan.Errors) > 0 {
		t.Fatalf("errors = %v, want none for a region the service does not use", plan.Errors)
	}
}

func TestDomainNoClusterServesIsACustomDomain(t *testing.T) {
	in := input(map[string]locofile.Service{webService: fileService()})
	in.Clusters = []Cluster{{Name: "cluster-bare", Region: testRegion}}
	onlyError(t, compute(t, in), ErrCustomDomain)
}

func TestMatchPlatformDomainPicksTheLongestSuffix(t *testing.T) {
	platformDomains := []string{"loco.test", "stage.loco.test"}
	platform, label, err := MatchPlatformDomain("api.stage.loco.test", platformDomains)
	if err != nil || platform != "stage.loco.test" || label != "api" {
		t.Fatalf("match = %q %q %v, want api under stage.loco.test", platform, label, err)
	}
	platform, label, err = MatchPlatformDomain("api.loco.test", platformDomains)
	if err != nil || platform != "loco.test" || label != "api" {
		t.Fatalf("match = %q %q %v, want api under loco.test", platform, label, err)
	}
}

func TestDomainMoreThanOneLabelUnderAPlatformDomainIsAnError(t *testing.T) {
	service := fileService()
	service.Domains = []string{"api.stage.example.com"}
	planErr := onlyError(t, compute(t, input(map[string]locofile.Service{webService: service})), ErrNotSingleLabel)
	if planErr.Path != pathDomains {
		t.Fatalf("error = %+v, want it at domains", planErr)
	}
}

func TestUnknownEnvironmentIsAnError(t *testing.T) {
	in := input(map[string]locofile.Service{webService: fileService()})
	in.FileEnvironments = []string{"prod", "qa"}
	planErr := onlyError(t, compute(t, in), ErrUnknownEnvironment)
	if planErr.Path != "environments.qa" || planErr.Service != "" {
		t.Fatalf("error = %+v, want environments.qa on the file", planErr)
	}
}

func TestMissingSecretIsAnError(t *testing.T) {
	service := fileService()
	service.Secrets = []string{sessionSecret, apiKeySecret}
	in := input(map[string]locofile.Service{webService: service})
	in.Secrets = map[string]map[string]int64{webService: {apiKeySecret: 1}}
	planErr := onlyError(t, compute(t, in), ErrMissingSecret)
	if planErr.Path != pathSecrets ||
		planErr.Err.Error() != "secret is not set for the service in the environment: SESSION_KEY" {
		t.Fatalf("error = %+v, want the missing name", planErr)
	}
}

func TestASecretAnotherServiceHoldsIsMissing(t *testing.T) {
	service := fileService()
	service.Secrets = []string{sessionSecret}
	in := input(map[string]locofile.Service{webService: service})
	in.Secrets = map[string]map[string]int64{workerService: {sessionSecret: 1}}
	planErr := onlyError(t, compute(t, in), ErrMissingSecret)
	if planErr.Service != webService {
		t.Fatalf("error = %+v, want web's SESSION_KEY missing although worker holds one", planErr)
	}
}

func TestSecretsOverThePlacementLimitAreAnError(t *testing.T) {
	service := fileService()
	service.Secrets = []string{sessionSecret, apiKeySecret}
	in := input(map[string]locofile.Service{webService: service})
	half := in.MaxSecretBytes / 2
	in.Secrets = map[string]map[string]int64{webService: {sessionSecret: half, apiKeySecret: half}}
	planErr := onlyError(t, compute(t, in), ErrSecretsTooLarge)
	if planErr.Service != webService || planErr.Path != pathSecrets {
		t.Fatalf("error = %+v, want web's secrets over the limit", planErr)
	}

	in.Secrets[webService][apiKeySecret] = half - int64(len(sessionSecret)+len(apiKeySecret))
	if plan := compute(t, in); len(plan.Errors) != 0 {
		t.Fatalf("errors at the limit = %v, want none", plan.Errors)
	}
}

func TestProvisionWaivesMissingSecretsOfAServiceThePlanCreates(t *testing.T) {
	service := fileService()
	service.Secrets = []string{sessionSecret}
	in := input(map[string]locofile.Service{webService: service})
	in.Provision = []string{webService}
	op := onlyOperation(t, compute(t, in))
	if op.Kind != KindCreate || op.Service != webService {
		t.Fatalf("operation = %+v, want web created", op)
	}

	live := Service{Name: webService, Partial: testPartial, State: liveState()}
	in = input(map[string]locofile.Service{webService: service}, live)
	in.Provision = []string{webService}
	onlyError(t, compute(t, in), ErrMissingSecret)
}

func TestDisabledRunningServiceIsADestructiveStop(t *testing.T) {
	live := Service{
		Name:           webService,
		Partial:        testPartial,
		Built:          true,
		RunningRegions: []string{testRegion},
		State:          liveState(),
	}
	in := input(map[string]locofile.Service{}, live)
	in.Disabled = []string{webService}
	op := onlyOperation(t, compute(t, in))
	if op.Kind != KindUpdate || !op.Destructive || op.NeedsDeploy {
		t.Fatalf("operation = %+v, want a destructive update", op)
	}
	if want := []Change{disabledChange}; !reflect.DeepEqual(want, op.Changes) {
		t.Fatalf("changes = %v, want %v", op.Changes, want)
	}
}

func TestDisabledStoppedServiceIsNotAnOperation(t *testing.T) {
	live := Service{Name: webService, Partial: testPartial, Built: true, State: liveState()}
	in := input(map[string]locofile.Service{}, live)
	in.Disabled = []string{webService}
	plan := compute(t, in)
	if len(plan.Operations) != 0 || len(plan.Errors) != 0 {
		t.Fatalf("plan = %+v, want nothing for a disabled service that is not running", plan)
	}
}

func TestStoppedServiceInTheFileIsEnabledAgain(t *testing.T) {
	cases := map[string]struct {
		built           bool
		wantNeedsDeploy bool
	}{
		"built":   {built: true, wantNeedsDeploy: false},
		"unbuilt": {built: false, wantNeedsDeploy: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			live := Service{Name: webService, Partial: testPartial, Built: tc.built, State: liveState()}
			op := onlyOperation(t, compute(t, input(map[string]locofile.Service{webService: fileService()}, live)))
			if op.Kind != KindUpdate || op.Destructive || op.NeedsDeploy != tc.wantNeedsDeploy {
				t.Fatalf("operation = %+v, want an update with needs_deploy %v", op, tc.wantNeedsDeploy)
			}
			if want := []Change{enabledChange}; !reflect.DeepEqual(want, op.Changes) {
				t.Fatalf("changes = %v, want %v", op.Changes, want)
			}
		})
	}
}

func TestOperationsAreSortedByServiceName(t *testing.T) {
	services := map[string]locofile.Service{webService: fileService(), apiService: fileService()}
	live := Service{
		Name:           oldService,
		Partial:        testPartial,
		Built:          true,
		RunningRegions: []string{testRegion},
		State:          liveState(),
	}
	plan := compute(t, input(services, live))
	names := make([]string, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		names = append(names, op.Service)
	}
	want := []string{apiService, oldService, webService}
	if !reflect.DeepEqual(want, names) {
		t.Fatalf("order = %v, want %v", names, want)
	}
}

func TestPlanRejectsEnabledAutoscaling(t *testing.T) {
	service := fileService()
	region := service.Regions[testRegion]
	region.Autoscaling = &locofile.Autoscaling{CPUTarget: new(int32(70))}
	service.Regions[testRegion] = region
	planErr := onlyError(
		t,
		compute(t, input(map[string]locofile.Service{webService: service})),
		locoControllerV1.ErrAutoscalingUnsupported,
	)
	if planErr.Path != testRegionPath {
		t.Fatalf("error path = %q, want %q", planErr.Path, testRegionPath)
	}
}
