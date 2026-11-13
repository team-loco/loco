// Package guardrails holds the request rules the proto definitions cannot express. The query
// server's validation interceptor enforces the proto rules first.
package guardrails

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

var (
	errNonCanonicalID      = errors.New("IDs must be lowercase UUIDs")
	errInvalidRegionFilter = errors.New("region filters must not be blank and may only end with a wildcard")
	errSinceTooOld         = errors.New("since is older than the maximum time range")
	errSinceInFuture       = errors.New("since is in the future")
	errTimestampRange      = errors.New("timestamp is outside nanosecond precision range")
	errReservedLabelKey    = errors.New("label keys in the loco. attribute namespace are reserved")
)

// Limits bounds what one query may ask for.
type Limits struct {
	DefaultLimit int32
	MaxLimit     int32
	MaxTimeRange time.Duration
}

const (
	reservedAttributePrefix = "loco."
	// keep in sync with the loco.io/ keys in k8sapi/labels and k8sapi/v1alpha1
	kubernetesLabelPrefix = "loco.io/"
)

// ValidateLogsRequest validates the query parameters for a logs request.
func ValidateLogsRequest(req *observabilityv1.QueryLogsRequest, limits Limits) error {
	if err := validateCanonicalIDs(req.GetWorkspaceId(), req.GetEnvironmentId(), req.GetResourceIds()); err != nil {
		return err
	}
	if err := validateLabels(req.GetLabels()); err != nil {
		return err
	}
	if err := validateRegions(req.GetRegions(), req.GetExcludedRegions()); err != nil {
		return err
	}
	return validateQueryTimeRange(req.GetStartTime(), req.GetEndTime(), limits.MaxTimeRange)
}

// ValidateMetricsRequest validates the query parameters for a metrics request.
func ValidateMetricsRequest(req *observabilityv1.QueryMetricsRequest, limits Limits) error {
	if err := validateCanonicalIDs(req.GetWorkspaceId(), req.GetEnvironmentId(), req.GetResourceIds()); err != nil {
		return err
	}
	return validateQueryTimeRange(req.GetStartTime(), req.GetEndTime(), limits.MaxTimeRange)
}

// ValidateTailRequest validates a tail logs request.
func ValidateTailRequest(req *observabilityv1.TailLogsRequest, limits Limits, now time.Time) error {
	if err := validateCanonicalIDs(req.GetWorkspaceId(), req.GetEnvironmentId(), req.GetResourceIds()); err != nil {
		return err
	}
	if err := validateLabels(req.GetLabels()); err != nil {
		return err
	}
	if err := validateRegions(req.GetRegions(), req.GetExcludedRegions()); err != nil {
		return err
	}
	if req.GetSince() == nil {
		return nil
	}
	since := req.GetSince().AsTime()
	if since.After(now) {
		return errSinceInFuture
	}
	if now.Sub(since) > limits.MaxTimeRange {
		return fmt.Errorf("%w of %v", errSinceTooOld, limits.MaxTimeRange)
	}
	return nil
}

func ValidateTraceScope(scope *observabilityv1.TraceScope) error {
	return validateCanonicalIDs(scope.GetWorkspaceId(), scope.GetEnvironmentId(), scope.GetResourceIds())
}

func ValidateListTracesRequest(req *observabilityv1.ListTracesRequest, limits Limits) error {
	if err := ValidateTraceScope(req.GetScope()); err != nil {
		return err
	}
	return validateQueryTimeRange(req.GetStartTime(), req.GetEndTime(), limits.MaxTimeRange)
}

// ClampLimit ensures the limit is within bounds.
func ClampLimit(requested int32, limits Limits) int32 {
	if requested <= 0 {
		return limits.DefaultLimit
	}
	if requested > limits.MaxLimit {
		return limits.MaxLimit
	}
	return requested
}

// validateCanonicalIDs rejects uppercase IDs. The proto uuid rule accepts them, but ClickHouse
// stores the lowercase form, so an uppercase ID would pass the permission check and match nothing.
func validateCanonicalIDs(workspaceID, environmentID string, resourceIDs []string) error {
	for _, id := range append([]string{workspaceID, environmentID}, resourceIDs...) {
		if id != strings.ToLower(id) {
			return errNonCanonicalID
		}
	}
	return nil
}

func validateLabels(labels map[string]string) error {
	for key := range labels {
		if strings.HasPrefix(key, reservedAttributePrefix) && !strings.HasPrefix(key, kubernetesLabelPrefix) {
			return fmt.Errorf("%w: %q", errReservedLabelKey, key)
		}
	}
	return nil
}

func validateQueryTimeRange(startTimestamp, endTimestamp *timestamppb.Timestamp, maxRange time.Duration) error {
	if err := startTimestamp.CheckValid(); err != nil {
		return err
	}
	if err := endTimestamp.CheckValid(); err != nil {
		return err
	}
	start := startTimestamp.AsTime()
	end := endTimestamp.AsTime()
	if !time.Unix(0, start.UnixNano()).Equal(start) || !time.Unix(0, end.UnixNano()).Equal(end) {
		return errTimestampRange
	}
	if end.Sub(start) > maxRange {
		return fmt.Errorf("time range exceeds maximum of %v", maxRange)
	}
	return nil
}

func validateRegions(groups ...[]string) error {
	for _, group := range groups {
		for _, value := range group {
			if strings.TrimSpace(value) == "" || strings.Contains(strings.TrimSuffix(value, "*"), "*") {
				return errInvalidRegionFilter
			}
		}
	}
	return nil
}
