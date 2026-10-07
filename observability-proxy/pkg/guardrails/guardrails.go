package guardrails

import (
	"errors"
	"fmt"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

var (
	errMetricNameRequired  = errors.New("metric_name is required")
	errTimeRangeRequired   = errors.New("start_time and end_time are required")
	errEndBeforeStart      = errors.New("end_time must be after start_time")
	errWorkspaceIDRequired = errors.New("workspace_id is required")
)

// ValidateLogsRequest validates and clamps the query parameters for a logs request.
func ValidateLogsRequest(req *observabilityv1.QueryLogsRequest, cfg *config.Config) error {
	if req.GetWorkspaceId() == "" {
		return errWorkspaceIDRequired
	}

	start := req.GetStartTime().AsTime()
	end := req.GetEndTime().AsTime()

	if err := validateTimeRange(start, end, cfg.MaxTimeRange); err != nil {
		return err
	}

	return nil
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
func ValidateTailRequest(req *observabilityv1.TailLogsRequest, _ *config.Config) error {
	if req.GetWorkspaceId() == "" {
		return errWorkspaceIDRequired
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

func isAllowedAggregation(agg string) bool {
	switch agg {
	case "avg", "sum", "min", "max", "p50", "p95", "p99":
		return true
	default:
		return false
	}
}
