package guardrails

import (
	"errors"
	"fmt"
	"strings"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

var (
	errMetricNameRequired  = errors.New("metric_name is required")
	errTimeRangeRequired   = errors.New("start_time and end_time are required")
	errEndBeforeStart      = errors.New("end_time must be after start_time")
	errWorkspaceIDRequired = errors.New("workspace_id is required")
	errSinceTooOld         = errors.New("since is older than the maximum time range")
	errSinceInFuture       = errors.New("since is in the future")
	errReservedLabelKey    = errors.New("label keys in the loco. attribute namespace are reserved")
)

const (
	reservedAttributePrefix = "loco."
	kubernetesLabelPrefix   = "loco.io/"
)

// ValidateLogsRequest validates and clamps the query parameters for a logs request.
func ValidateLogsRequest(req *observabilityv1.QueryLogsRequest, cfg *config.Config) error {
	if req.GetWorkspaceId() == "" {
		return errWorkspaceIDRequired
	}
	if err := validateLabels(req.GetLabels()); err != nil {
		return err
	}

	start := req.GetStartTime().AsTime()
	end := req.GetEndTime().AsTime()

	return validateTimeRange(start, end, cfg.MaxTimeRange)
}

// ValidateMetricsRequest validates and clamps the query parameters for a metrics request.
func ValidateMetricsRequest(req *observabilityv1.QueryMetricsRequest, cfg *config.Config) error {
	if req.GetWorkspaceId() == "" {
		return errWorkspaceIDRequired
	}
	if req.GetMetricName() == "" {
		return errMetricNameRequired
	}

	start := req.GetStartTime().AsTime()
	end := req.GetEndTime().AsTime()

	if err := validateTimeRange(start, end, cfg.MaxTimeRange); err != nil {
		return err
	}

	agg := req.GetAggregation()
	if !isAllowedAggregation(agg) {
		return fmt.Errorf("unsupported aggregation: %s (allowed: avg, sum, min, max, p50, p95, p99)", agg)
	}

	return nil
}

// ValidateTailRequest validates a tail logs request.
func ValidateTailRequest(req *observabilityv1.TailLogsRequest, cfg *config.Config, now time.Time) error {
	if req.GetWorkspaceId() == "" {
		return errWorkspaceIDRequired
	}
	if err := validateLabels(req.GetLabels()); err != nil {
		return err
	}
	if req.GetSince() == nil {
		return nil
	}
	since := req.GetSince().AsTime()
	if since.After(now) {
		return errSinceInFuture
	}
	if now.Sub(since) > cfg.MaxTimeRange {
		return fmt.Errorf("%w of %v", errSinceTooOld, cfg.MaxTimeRange)
	}
	return nil
}

// ClampLimit ensures the limit is within bounds.
func ClampLimit(requested int32, cfg *config.Config) int32 {
	if requested <= 0 {
		return cfg.DefaultLimit
	}
	if requested > cfg.MaxLimit {
		return cfg.MaxLimit
	}
	return requested
}

func validateTimeRange(start, end time.Time, maxRange time.Duration) error {
	if start.IsZero() || end.IsZero() {
		return errTimeRangeRequired
	}
	if end.Before(start) {
		return errEndBeforeStart
	}
	if end.Sub(start) > maxRange {
		return fmt.Errorf("time range exceeds maximum of %v", maxRange)
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

func isAllowedAggregation(agg string) bool {
	switch agg {
	case "avg", "sum", "min", "max", "p50", "p95", "p99":
		return true
	default:
		return false
	}
}
