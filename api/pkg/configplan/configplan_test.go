package configplan

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/internal/locofile"
)

const (
	testPartial    = webService
	testRegion     = "us-east-1"
	testImage      = "ghcr.io/acme/worker:1.4.2"
	webService     = "web"
	workerService  = "worker"
	apiService     = "api"
	oldService     = "old"
	otherPartial   = "backend"
	sessionSecret  = "SESSION_KEY"
	logLevelKey    = "LOG_LEVEL"
	logLevelPath   = "env." + logLevelKey
	testRegionPath = "regions." + testRegion
	logLevelInfo   = "info"
	testCPU        = "250m"
	testMemory     = "512Mi"
	testPinned     = "ghcr.io/acme/worker@sha256:7781a08afca1adb11b6294ca81ee6e04f9fc677f4f04c9d9daf2b0e068f5e89a"
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
		Dockerfile: defaultDockerfile,
		Port:       &port,
		Routing:    &locofile.Routing{},
		Domains:    []string{"app.example.com"},
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
		Dockerfile: defaultDockerfile,
		Context:    defaultContext,
		Port:       3000,
		Health:     DefaultHealth(testDefaults()),
		Routing:    &Routing{PathPrefix: "/", IdleTimeout: 60},
		Domains:    []string{"app.example.com"},
		Env:        map[string]string{logLevelKey: logLevelInfo},
		Regions:    map[string]Region{testRegion: {CPU: testCPU, Memory: testMemory, MinReplicas: 1, MaxReplicas: 3}},
	}
}

func input(services map[string]locofile.Service, live ...Service) Input {
	return Input{
		Partial:      testPartial,
		Services:     services,
		Environments: []string{"prod"},
		Regions:      []string{testRegion},
		Live:         live,
		Defaults:     testDefaults(),
		Images:       map[string]ImageResult{testImage: {Pinned: testPinned}},
	}
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
	live := Service{Name: webService, Partial: testPartial, Built: true, State: liveState()}
	plan := compute(t, input(map[string]locofile.Service{webService: fileService()}, live))
	if len(plan.Operations) != 0 || len(plan.Errors) != 0 {
		t.Fatalf("plan = %+v, want no operations for a service that matches the file", plan)
	}
}

func TestNewServiceIsACreateListingEveryField(t *testing.T) {
	plan := compute(t, input(map[string]locofile.Service{webService: fileService()}))
	op := onlyOperation(t, plan)
	if op.Kind != KindCreate || !op.NeedsDeploy || op.Destructive {
		t.Fatalf("operation = %+v, want a create that needs a deploy", op)
	}
	want := []Change{
		{Path: "context", After: defaultContext},
		{Path: "dockerfile", After: defaultDockerfile},
		{Path: "domains", After: "[app.example.com]"},
		{Path: logLevelPath, After: logLevelInfo},
		{Path: "health.failThreshold", After: "3"},
		{Path: "health.interval", After: "30"},
		{Path: "health.path", After: "/health"},
		{Path: "health.startupGracePeriod", After: "0"},
		{Path: "health.timeout", After: "5"},
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
	cpuTarget := int32(70)
	region := service.Regions[testRegion]
	region.Autoscaling = &locofile.Autoscaling{CPUTarget: &cpuTarget}
	service.Regions[testRegion] = region
	live := Service{Name: webService, Partial: testPartial, Built: true, State: liveState()}
	in := input(map[string]locofile.Service{webService: service}, live)
	in.Secrets = []string{sessionSecret}

	op := onlyOperation(t, compute(t, in))
	if op.Kind != KindUpdate || op.NeedsDeploy || op.Destructive {
		t.Fatalf("operation = %+v, want an update", op)
	}
	want := []Change{
		{Path: logLevelPath, Before: logLevelInfo, After: "debug"},
		{Path: "regions.us-east-1.autoscaling.cpuTarget", After: "70"},
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
	live := Service{Name: webService, Partial: testPartial, Built: true, State: liveState()}
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
	live := Service{Name: webService, Built: true, State: liveState()}
	op := onlyOperation(t, compute(t, input(map[string]locofile.Service{webService: fileService()}, live)))
	if op.Kind != KindImport || len(op.Changes) != 0 || op.Destructive {
		t.Fatalf("operation = %+v, want an import without changes", op)
	}
}

func TestOwnedServiceAbsentFromTheFileIsADestructiveDelete(t *testing.T) {
	live := Service{Name: oldService, Partial: testPartial, Built: true, State: liveState()}
	op := onlyOperation(t, compute(t, input(map[string]locofile.Service{}, live)))
	if op.Kind != KindDelete || !op.Destructive || op.Service != oldService {
		t.Fatalf("operation = %+v, want a destructive delete of old", op)
	}
}

func TestAnotherPartialsServiceIsIgnoredWhenAbsentFromTheFile(t *testing.T) {
	live := Service{Name: apiService, Partial: otherPartial, Built: true, State: liveState()}
	plan := compute(t, input(map[string]locofile.Service{}, live))
	if len(plan.Operations) != 0 || len(plan.Errors) != 0 {
		t.Fatalf("plan = %+v, want nothing", plan)
	}
}

func TestAnotherPartialsServiceNamedInTheFileIsAnError(t *testing.T) {
	live := Service{Name: webService, Partial: otherPartial, Built: true, State: liveState()}
	plan := compute(t, input(map[string]locofile.Service{webService: fileService()}, live))
	planErr := onlyError(t, plan, ErrOwnedByOtherPartial)
	if planErr.Service != webService {
		t.Fatalf("error service = %q, want web", planErr.Service)
	}
}

func TestSourceServiceWithoutABuildNeedsADeploy(t *testing.T) {
	live := Service{Name: webService, Partial: testPartial, State: liveState()}
	op := onlyOperation(t, compute(t, input(map[string]locofile.Service{webService: fileService()}, live)))
	if op.Kind != KindUpdate || len(op.Changes) != 0 || !op.NeedsDeploy {
		t.Fatalf("operation = %+v, want an update with no changes that needs a deploy", op)
	}
}

func TestChangedBuildInputsNeedADeploy(t *testing.T) {
	service := fileService()
	service.Dockerfile = "build/Dockerfile"
	service.Context = "services/web"
	live := Service{Name: webService, Partial: testPartial, Built: true, State: liveState()}
	op := onlyOperation(t, compute(t, input(map[string]locofile.Service{webService: service}, live)))
	if op.Kind != KindUpdate || !op.NeedsDeploy {
		t.Fatalf("operation = %+v, want an update that needs a deploy", op)
	}
	want := []Change{
		{Path: "context", Before: defaultContext, After: "services/web"},
		{Path: "dockerfile", Before: defaultDockerfile, After: "build/Dockerfile"},
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
			s.Routing = &locofile.Routing{PathPrefix: "api"}
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
	service.Secrets = []string{sessionSecret, "API_KEY"}
	in := input(map[string]locofile.Service{webService: service})
	in.Secrets = []string{"API_KEY"}
	planErr := onlyError(t, compute(t, in), ErrMissingSecret)
	if planErr.Path != "secrets" || planErr.Err.Error() != "secret is not set in the environment: SESSION_KEY" {
		t.Fatalf("error = %+v, want the missing name", planErr)
	}
}

func TestOperationsAreSortedByServiceName(t *testing.T) {
	services := map[string]locofile.Service{webService: fileService(), apiService: fileService()}
	live := Service{Name: oldService, Partial: testPartial, Built: true, State: liveState()}
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
