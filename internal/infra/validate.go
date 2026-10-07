package infra

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	loco "github.com/team-loco/loco/sdk/go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	minimumApplicationPort   = 1024
	maximumPort              = 65535
	maximumDescriptionBytes  = 256
	maximumNameBytes         = 63
	maximumHostnameBytes     = 253
	maximumVariableBytes     = 64 << 10
	minimumAutoscalingTarget = 1
	maximumAutoscalingTarget = 100
	minimumHealthInterval    = 5
	maximumHealthTimeout     = 60
	maximumHealthFailures    = 10
	maximumStartupGrace      = 180
	maximumSampleRate        = 1
)

// names follow Kubernetes DNS-1035 service-label rules; hostname labels follow DNS-1123.
var (
	namePattern     = regexp.MustCompile(fmt.Sprintf(`^[a-z](?:[a-z0-9-]{0,%d}[a-z0-9])?$`, maximumNameBytes-2))
	dnsPattern      = regexp.MustCompile(fmt.Sprintf(`^[a-z0-9](?:[a-z0-9-]{0,%d}[a-z0-9])?$`, maximumNameBytes-2))
	envPattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	quantityPattern = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)(?:[numkMGTPE]|[KMGTPE]i)?$`)
)

func ValidateManifest(manifest *loco.Manifest) error {
	if manifest == nil || manifest.GetVersion() != loco.ProtocolVersion || manifest.GetStack() == nil {
		return errUnsupportedInfrastructureProtocolVersion
	}
	stack := manifest.GetStack()
	if (stack.GetVersion() != 0 && stack.GetVersion() != loco.ProtocolVersion) ||
		!namePattern.MatchString(stack.GetName()) {
		return errStackRequiresProtocolVersionAndA
	}
	keys := make(map[string]bool, len(stack.GetServices()))
	names := make(map[string]bool, len(stack.GetServices()))
	for _, service := range stack.GetServices() {
		if service == nil {
			return errServiceIsRequired
		}
		if err := normalizeService(service); err != nil {
			return fmt.Errorf("service %q: %w", service.GetKey(), err)
		}
		if keys[service.GetKey()] || names[effectiveName(service)] {
			return fmt.Errorf("duplicate service key or name %q", service.GetKey())
		}
		keys[service.GetKey()] = true
		names[effectiveName(service)] = true
	}
	return nil
}

func normalizeService(service *loco.Service) error {
	if !namePattern.MatchString(service.GetKey()) ||
		(service.GetName() != "" && !namePattern.MatchString(service.GetName())) {
		return errKeyAndNameMustBeLowercase
	}
	if len(service.GetDescription()) > maximumDescriptionBytes {
		return errDescriptionExceedsCharacters
	}
	switch service.GetSource().(type) {
	case *infrav1.ServiceManifest_Docker:
		build := service.GetDocker()
		if build == nil {
			return errDockerSourceIsRequired
		}
		if build.GetContext() == "" || build.GetDockerfile() == "" {
			return errDockerContextAndDockerfileAreRequired
		}
	case *infrav1.ServiceManifest_Image:
		if service.GetImage() == "" {
			return errImageSourceIsRequired
		}
	default:
		return errProvideExactlyOneDockerOrImage
	}
	spec := service.GetSpec().GetService()
	if spec == nil {
		return errServiceSpecIsRequired
	}
	if err := validateRouting(service, spec); err != nil {
		return err
	}
	if err := validateRegions(spec); err != nil {
		return err
	}
	if err := validateHealth(spec.GetHealthCheck()); err != nil {
		return err
	}
	if err := validateVariables(service.GetVariables()); err != nil {
		return err
	}
	if spec.GetObservability() == nil {
		spec.Observability = &loco.Observability{}
	}
	return validateObservability(spec.GetObservability())
}

func validateRouting(service *loco.Service, spec *loco.ServiceSpec) error {
	routing := spec.GetRouting()
	if routing.GetPort() != 0 && (routing.GetPort() < minimumApplicationPort || routing.GetPort() > maximumPort) {
		return errRoutingPortMustBeBetweenAnd
	}
	if routing.GetPathPrefix() != "" && !strings.HasPrefix(routing.GetPathPrefix(), "/") {
		return errRoutingPathprefixMustStartWith
	}
	if routing.GetIdleTimeout() < 0 {
		return errRoutingIdletimeoutCannotBeNegative
	}
	if service.GetHostname() != "" {
		host := strings.ToLower(service.GetHostname())
		if len(host) > maximumHostnameBytes || len(strings.Split(host, ".")) < 2 {
			return errDomainRequiresAFullHostname
		}
		for _, label := range strings.Split(host, ".") {
			if !dnsPattern.MatchString(label) {
				return errDomainContainsAnInvalidDNSLabel
			}
		}
	}
	return nil
}

func validateRegions(spec *loco.ServiceSpec) error {
	if len(spec.GetRegions()) == 0 {
		return errRegionsAreRequired
	}
	primary := 0
	for name, region := range spec.GetRegions() {
		if !namePattern.MatchString(name) {
			return fmt.Errorf("invalid region name %q", name)
		}
		if region == nil || !region.GetEnabled() {
			return fmt.Errorf("region %q must be enabled or omitted", name)
		}
		if region.GetPrimary() {
			primary++
		}
		if (region.GetCpu() != "" && !positiveQuantity(region.GetCpu())) ||
			(region.GetMemory() != "" && !positiveQuantity(region.GetMemory())) {
			return fmt.Errorf("region %q requires positive CPU and memory quantities", name)
		}
		if region.GetMinReplicas() < 0 || region.GetMaxReplicas() < 0 ||
			(region.GetMaxReplicas() != 0 && region.GetMaxReplicas() < region.GetMinReplicas()) {
			return fmt.Errorf("region %q requires 1 <= minReplicas <= maxReplicas", name)
		}
		if region.GetScalers().GetEnabled() {
			if err := validateAutoscaling(region.GetScalers()); err != nil {
				return fmt.Errorf("region %q: %w", name, err)
			}
		}
	}
	if primary != 1 {
		return errExactlyOneConfiguredRegionMustBe
	}
	return nil
}

func positiveQuantity(value string) bool {
	return quantityPattern.MatchString(value) && strings.ContainsAny(value, "123456789")
}

func hasField(message proto.Message, name protoreflect.Name) bool {
	value := message.ProtoReflect()
	return value.Has(value.Descriptor().Fields().ByName(name))
}

func validateAutoscaling(scaler *loco.Autoscaling) error {
	cpu := hasField(scaler, "cpu_target")
	memory := hasField(scaler, "memory_target")
	if cpu == memory {
		return errAutoscalingRequiresExactlyOneCputargetOr
	}
	if cpu && (scaler.GetCpuTarget() < minimumAutoscalingTarget || scaler.GetCpuTarget() > maximumAutoscalingTarget) {
		return errAutoscalingTargetMustBeBetweenAnd
	}
	if memory &&
		(scaler.GetMemoryTarget() < minimumAutoscalingTarget || scaler.GetMemoryTarget() > maximumAutoscalingTarget) {
		return errAutoscalingTargetMustBeBetweenAnd
	}
	return nil
}
func validateHealth(health *loco.Health) error {
	if health == nil {
		return nil
	}
	if health.GetPath() != "" && !strings.HasPrefix(health.GetPath(), "/") {
		return errHealthPathMustStartWith
	}
	if (health.GetIntervalSeconds() != 0 && health.GetIntervalSeconds() < minimumHealthInterval) ||
		health.GetTimeoutSeconds() < 0 ||
		health.GetTimeoutSeconds() > maximumHealthTimeout ||
		health.GetFailureThreshold() < 0 ||
		health.GetFailureThreshold() > maximumHealthFailures ||
		health.GetInitialDelaySeconds() < 0 ||
		health.GetInitialDelaySeconds() > maximumStartupGrace {
		return errHealthTimingAndFailureThresholdsAre
	}
	return nil
}

func validateVariables(variables map[string]*loco.Variable) error {
	for name, variable := range variables {
		if !envPattern.MatchString(name) {
			return fmt.Errorf("invalid variable name %q", name)
		}
		if variable == nil {
			return fmt.Errorf("variable %q requires an expression", name)
		}
		switch variable.GetExpression().(type) {
		case *infrav1.Variable_Literal:
			value := variable.GetLiteral()
			if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || len(value) > maximumVariableBytes {
				return fmt.Errorf("variable %q must contain UTF-8 text without NUL, at most 64 KiB", name)
			}
		case *infrav1.Variable_Secret:
			if !namePattern.MatchString(variable.GetSecret()) {
				return fmt.Errorf("secret variable %q requires a secret name", name)
			}
		case *infrav1.Variable_Preserve:
			if !variable.GetPreserve() {
				return fmt.Errorf("preserve expression for %q must be true", name)
			}
		default:
			return fmt.Errorf("variable %q requires an expression", name)
		}
	}
	return nil
}
func validateObservability(obs *loco.Observability) error {
	if obs == nil {
		return nil
	}
	logging := obs.GetLogging()
	if logging.GetRetentionPeriod() != "" {
		retention := strings.TrimSuffix(logging.GetRetentionPeriod(), "d")
		if retention != logging.GetRetentionPeriod() {
			retention += "h"
		}
		if duration, err := time.ParseDuration(retention); err != nil || duration <= 0 {
			return errLoggingRetentionMustBeAPositive
		}
	}
	metrics := obs.GetMetrics()
	invalidPath := metrics.GetPath() != "" && !strings.HasPrefix(metrics.GetPath(), "/")
	invalidPort := metrics.GetPort() < 0 || metrics.GetPort() > maximumPort
	if metrics.GetEnabled() && (invalidPath || invalidPort) {
		return errMetricsRequiresAValidPathAnd
	}
	if hasField(obs.GetTracing(), "sample_rate") {
		rate := obs.GetTracing().GetSampleRate()
		if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > maximumSampleRate {
			return errTracingSamplerateMustBeBetweenAnd
		}
	}
	return nil
}

func effectiveName(service *loco.Service) string {
	if service.GetName() != "" {
		return service.GetName()
	}
	return service.GetKey()
}
