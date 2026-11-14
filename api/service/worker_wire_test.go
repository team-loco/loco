package service

import (
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func workerHealthCheck() *deploymentv1.HealthCheckConfig {
	return &deploymentv1.HealthCheckConfig{Path: "/healthz", IntervalSeconds: 5, TimeoutSeconds: 1, FailureThreshold: 1}
}

func TestWireRejectsTrafficSettingsWithoutAPort(t *testing.T) {
	health := workerHealthCheck()
	regions := map[string]*resourcev1.RegionTarget{testRegion: {Enabled: true}}
	create := func(service *resourcev1.ServiceSpec) *resourcev1.CreateResourceRequest {
		return &resourcev1.CreateResourceRequest{
			WorkspaceId: "01a1dbb5-4966-72e2-a92b-a2560006942a",
			Name:        "worker",
			Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
			Spec:        &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: service}},
		}
	}
	domain := create(&resourcev1.ServiceSpec{Regions: regions})
	domain.Domain = &domainv1.DomainInput{
		DomainSource:   domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED,
		Subdomain:      new("worker"),
		PlatformDomain: new("example.com"),
	}
	build := &deploymentv1.BuildSource{Type: buildSourceTypeImage}
	prefixed := &resourcev1.ServiceSpec{Regions: regions, Routing: &resourcev1.RoutingConfig{PathPrefix: "/"}}
	for name, tc := range map[string]struct {
		message proto.Message
		valid   bool
	}{
		"worker":                {create(&resourcev1.ServiceSpec{Regions: regions}), true},
		"worker deployment":     {&deploymentv1.ServiceDeploymentSpec{Build: build}, true},
		"domain":                {domain, false},
		"path prefix":           {create(prefixed), false},
		"resource health check": {create(&resourcev1.ServiceSpec{Regions: regions, HealthCheck: health}), false},
		"deployment health":     {&deploymentv1.ServiceDeploymentSpec{Build: build, HealthCheck: health}, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := protovalidate.Validate(tc.message)
			if (err == nil) != tc.valid {
				t.Fatalf("validate = %v, want valid %v", err, tc.valid)
			}
		})
	}
}
