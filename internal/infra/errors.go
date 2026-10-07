package infra

import "errors"

var (
	errUnsupportedInfrastructureProtocolVersion = errors.New("unsupported infrastructure protocol version")
	errStackRequiresProtocolVersionAndA         = errors.New(
		"stack requires protocol version 1 and a lowercase name of at most 63 characters",
	)
	errServiceIsRequired         = errors.New("service is required")
	errKeyAndNameMustBeLowercase = errors.New(
		"key and name must be lowercase names of at most 63 characters",
	)
	errDescriptionExceedsCharacters             = errors.New("description exceeds 256 characters")
	errDockerSourceIsRequired                   = errors.New("docker source is required")
	errDockerContextAndDockerfileAreRequired    = errors.New("docker context and Dockerfile are required")
	errImageSourceIsRequired                    = errors.New("image source is required")
	errProvideExactlyOneDockerOrImage           = errors.New("provide exactly one docker or image source")
	errServiceSpecIsRequired                    = errors.New("service spec is required")
	errRoutingPortMustBeBetweenAnd              = errors.New("routing port must be between 1024 and 65535")
	errRoutingPathprefixMustStartWith           = errors.New("routing pathPrefix must start with /")
	errRoutingIdletimeoutCannotBeNegative       = errors.New("routing idleTimeout cannot be negative")
	errDomainRequiresAFullHostname              = errors.New("domain requires a full hostname")
	errDomainContainsAnInvalidDNSLabel          = errors.New("domain contains an invalid DNS label")
	errRegionsAreRequired                       = errors.New("regions are required")
	errExactlyOneConfiguredRegionMustBe         = errors.New("exactly one configured region must be primary")
	errAutoscalingRequiresExactlyOneCputargetOr = errors.New(
		"autoscaling requires exactly one cpuTarget or memoryTarget",
	)
	errAutoscalingTargetMustBeBetweenAnd   = errors.New("autoscaling target must be between 1 and 100")
	errHealthPathMustStartWith             = errors.New("health path must start with /")
	errHealthTimingAndFailureThresholdsAre = errors.New(
		"health timing and failure thresholds are outside supported bounds",
	)
	errLoggingRetentionMustBeAPositive   = errors.New("logging retention must be a positive duration")
	errMetricsRequiresAValidPathAnd      = errors.New("metrics requires a valid path and port")
	errTracingSamplerateMustBeBetweenAnd = errors.New("tracing sampleRate must be between 0 and 1")
	errJSONTooLarge                      = errors.New("JSON document exceeds the size limit")
)
