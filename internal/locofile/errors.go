package locofile

import "errors"

var (
	ErrNotSupportedYet        = errors.New("not supported yet")
	ErrUnsupportedVersion     = errors.New("version must be 1")
	ErrPartialRequired        = errors.New("partial is required")
	ErrNoServices             = errors.New("services is required; services: {} removes every service of the partial")
	ErrInvalidName            = errors.New("must be a DNS label of 1 to 63 lowercase letters, digits and hyphens")
	ErrImageWithBuild         = errors.New("image cannot be combined with dockerfile or context")
	ErrPortRequired           = errors.New("port is required when routing is set")
	ErrNoRegions              = errors.New("at least one region is required")
	ErrRegionIncomplete       = errors.New("cpu, memory, replicas.min and replicas.max are required")
	ErrAutoscalingTarget      = errors.New("autoscaling needs exactly one of cpuTarget or memoryTarget")
	ErrEnvironmentsInOverride = errors.New("environments cannot be nested inside an environment override")
	ErrNotAnObject            = errors.New("must be a mapping")
)
