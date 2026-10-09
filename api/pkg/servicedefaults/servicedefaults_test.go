package servicedefaults

import (
	"errors"
	"testing"
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
		"relative prefix":              {func(d *Defaults) { d.PathPrefix = "app" }, errPathPrefix},
		"zero idle timeout":            {func(d *Defaults) { d.IdleTimeout = 0 }, errIdleTimeout},
		"zero port":                    {func(d *Defaults) { d.Port = 0 }, errPort},
		"port above the range":         {func(d *Defaults) { d.Port = 65536 }, errPort},
		"relative health path":         {func(d *Defaults) { d.HealthPath = "health" }, errHealthPath},
		"zero health interval":         {func(d *Defaults) { d.HealthInterval = 0 }, errHealthTiming},
		"zero health timeout":          {func(d *Defaults) { d.HealthTimeout = 0 }, errHealthTiming},
		"zero fail threshold":          {func(d *Defaults) { d.HealthFailThreshold = 0 }, errHealthTiming},
		"negative grace period":        {func(d *Defaults) { d.HealthStartupGracePeriod = -1 }, errHealthGrace},
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
