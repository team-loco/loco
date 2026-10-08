package servicedefaults

import (
	"errors"
	"fmt"
	"strings"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	ErrInvalidCPU       = errors.New("invalid CPU quantity")
	ErrInvalidMemory    = errors.New("invalid memory quantity")
	errInvalidResources = errors.New("the default resources are not accepted by the controller")
	errPathPrefix       = errors.New("the default path prefix must start with '/'")
	errIdleTimeout      = errors.New("the default idle timeout must be positive")
)

type Defaults struct {
	CPU         string
	Memory      string
	MinReplicas int32
	MaxReplicas int32
	PathPrefix  string
	IdleTimeout int32
}

func (d Defaults) Validate() error {
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
