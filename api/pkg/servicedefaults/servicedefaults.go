package servicedefaults

import (
	"errors"
	"fmt"
	"strings"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const maxPort = 65535

var (
	ErrInvalidCPU       = errors.New("invalid CPU quantity")
	ErrInvalidMemory    = errors.New("invalid memory quantity")
	errInvalidResources = errors.New("the default resources are not accepted by the controller")
	errPathPrefix       = errors.New("the default path prefix must start with '/'")
	errIdleTimeout      = errors.New("the default idle timeout must be positive")
	errPort             = errors.New("the default port must be between 1 and 65535")
	errHealthPath       = errors.New("the default health path must start with '/'")
	errHealthTiming     = errors.New("the default health interval, timeout and fail threshold must be positive")
	errHealthGrace      = errors.New("the default health startup grace period cannot be negative")
)

// Defaults is what the API fills into a service when the file or request omits it.
type Defaults struct {
	CPU                      string
	Memory                   string
	MinReplicas              int32
	MaxReplicas              int32
	PathPrefix               string
	IdleTimeout              int32
	Port                     int32
	HealthPath               string
	HealthInterval           int32
	HealthTimeout            int32
	HealthFailThreshold      int32
	HealthStartupGracePeriod int32
}

func (d Defaults) Validate() error {
	if d.Port < 1 || d.Port > maxPort {
		return errPort
	}
	if !strings.HasPrefix(d.HealthPath, "/") {
		return errHealthPath
	}
	if d.HealthInterval < 1 || d.HealthTimeout < 1 || d.HealthFailThreshold < 1 {
		return errHealthTiming
	}
	if d.HealthStartupGracePeriod < 0 {
		return errHealthGrace
	}
	if _, err := ParseCPU(d.CPU); err != nil {
		return err
	}
	if _, err := ParseMemory(d.Memory); err != nil {
		return err
	}
	resources := locoControllerV1.ResourcesSpec{
		CPU:    d.CPU,
		Memory: d.Memory,
		Replicas: locoControllerV1.ReplicasSpec{
			Min: d.MinReplicas,
			Max: d.MaxReplicas,
		},
	}
	if err := resources.Validate(); err != nil {
		return fmt.Errorf("%w: %w", errInvalidResources, err)
	}
	if !strings.HasPrefix(d.PathPrefix, "/") {
		return errPathPrefix
	}
	if d.IdleTimeout < 1 {
		return errIdleTimeout
	}
	return nil
}

func ParseCPU(value string) (resource.Quantity, error) {
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("%w %q: %w", ErrInvalidCPU, value, err)
	}
	return quantity, nil
}

func ParseMemory(value string) (resource.Quantity, error) {
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("%w %q: %w", ErrInvalidMemory, value, err)
	}
	return quantity, nil
}
