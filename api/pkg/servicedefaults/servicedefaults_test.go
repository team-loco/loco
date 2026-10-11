package servicedefaults

import (
	"errors"
	"testing"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func validDefaults() Defaults {
	return Defaults{
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

func TestValidateAcceptsValidDefaults(t *testing.T) {
	if err := validDefaults().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsInvalidDefaults(t *testing.T) {
	cases := map[string]struct {
		change func(*Defaults)
		want   error
	}{
		"unparsable cpu":               {func(d *Defaults) { d.CPU = "lots" }, ErrInvalidCPU},
		"zero cpu":                     {func(d *Defaults) { d.CPU = "0" }, errInvalidResources},
		"cpu below the controller min": {func(d *Defaults) { d.CPU = "50m" }, errInvalidResources},
		"cpu above the controller max": {func(d *Defaults) { d.CPU = "4" }, errInvalidResources},
		"unparsable memory":            {func(d *Defaults) { d.Memory = "plenty" }, ErrInvalidMemory},
		"negative memory":              {func(d *Defaults) { d.Memory = "-1Mi" }, errInvalidResources},
		"memory above the controller":  {func(d *Defaults) { d.Memory = "8Gi" }, errInvalidResources},
		"zero min":                     {func(d *Defaults) { d.MinReplicas = 0 }, errInvalidResources},
		"max below min":                {func(d *Defaults) { d.MaxReplicas = 0 }, errInvalidResources},
		"max above the controller max": {func(d *Defaults) { d.MaxReplicas = 12 }, errInvalidResources},
		"relative prefix":              {func(d *Defaults) { d.PathPrefix = "app" }, errInvalidRoute},
		"zero idle timeout":            {func(d *Defaults) { d.IdleTimeout = 0 }, errInvalidRoute},
		"privileged port":              {func(d *Defaults) { d.Port = 80 }, errInvalidPort},
		"port above the range":         {func(d *Defaults) { d.Port = 65536 }, errInvalidPort},
		"relative health path":         {func(d *Defaults) { d.HealthPath = "health" }, errInvalidHealth},
		"short health interval":        {func(d *Defaults) { d.HealthInterval = 1 }, errInvalidHealth},
		"long health timeout":          {func(d *Defaults) { d.HealthTimeout = 61 }, errInvalidHealth},
		"high fail threshold":          {func(d *Defaults) { d.HealthFailThreshold = 11 }, errInvalidHealth},
		"long grace period":            {func(d *Defaults) { d.HealthStartupGracePeriod = 181 }, errInvalidHealth},
		"negative grace period":        {func(d *Defaults) { d.HealthStartupGracePeriod = -1 }, errInvalidHealth},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			defaults := validDefaults()
			tc.change(&defaults)
			if err := defaults.Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate = %v, want %v", err, tc.want)
			}
		})
	}
}

func applicationFor(d Defaults) *locoControllerV1.ApplicationSpec {
	health := d.HealthCheck()
	return &locoControllerV1.ApplicationSpec{
		ResourceID:  "resource",
		WorkspaceID: "workspace",
		Type:        "SERVICE",
		ServiceSpec: &locoControllerV1.ServiceSpec{
			Deployment: &locoControllerV1.ServiceDeploymentSpec{
				Image:       "nginx:1.27",
				Port:        d.Port,
				HealthCheck: &health,
			},
			Resources: &locoControllerV1.ResourcesSpec{
				CPU:      d.CPU,
				Memory:   d.Memory,
				Replicas: locoControllerV1.ReplicasSpec{Min: d.MinReplicas, Max: d.MaxReplicas},
			},
			Routing: &locoControllerV1.RoutingSpec{
				HostName:    "app.loco.test",
				PathPrefix:  d.PathPrefix,
				IdleTimeout: d.IdleTimeout,
			},
		},
	}
}

func TestValidateAgreesWithTheApplicationValidator(t *testing.T) {
	boundaries := map[string]func(*Defaults){
		"port 1023":         func(d *Defaults) { d.Port = 1023 },
		"port 1024":         func(d *Defaults) { d.Port = 1024 },
		"port 65535":        func(d *Defaults) { d.Port = 65535 },
		"interval 4":        func(d *Defaults) { d.HealthInterval = 4 },
		"interval 5":        func(d *Defaults) { d.HealthInterval = 5 },
		"timeout 0":         func(d *Defaults) { d.HealthTimeout = 0 },
		"timeout 60":        func(d *Defaults) { d.HealthTimeout = 60 },
		"timeout 61":        func(d *Defaults) { d.HealthTimeout = 61 },
		"fail threshold 10": func(d *Defaults) { d.HealthFailThreshold = 10 },
		"fail threshold 11": func(d *Defaults) { d.HealthFailThreshold = 11 },
		"grace 180":         func(d *Defaults) { d.HealthStartupGracePeriod = 180 },
		"grace 181":         func(d *Defaults) { d.HealthStartupGracePeriod = 181 },
		"grace -1":          func(d *Defaults) { d.HealthStartupGracePeriod = -1 },
		"replicas max 10":   func(d *Defaults) { d.MaxReplicas = 10 },
		"replicas max 11":   func(d *Defaults) { d.MaxReplicas = 11 },
		"idle timeout 0":    func(d *Defaults) { d.IdleTimeout = 0 },
	}
	for name, change := range boundaries {
		t.Run(name, func(t *testing.T) {
			defaults := validDefaults()
			change(&defaults)
			defaultsErr := defaults.Validate()
			applicationErr := applicationFor(defaults).Validate()
			if (defaultsErr == nil) != (applicationErr == nil) {
				t.Errorf("defaults: %v, application: %v", defaultsErr, applicationErr)
			}
		})
	}
}
