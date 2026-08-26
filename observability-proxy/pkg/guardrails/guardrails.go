package guardrails

import (
	"fmt"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

// ValidateLogsRequest validates and clamps the query parameters for a logs request.
func ValidateLogsRequest(req *observabilityv1.QueryLogsRequest, cfg *config.Config) error {
	if req.GetWorkspaceId() == "" {
		return fmt.Errorf("workspace_id is required")
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
		return fmt.Errorf("workspace_id is required")
	}
	if req.GetMetricName() == "" {
		return fmt.Errorf("metric_name is required")
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
func ValidateTailRequest(req *observabilityv1.TailLogsRequest, cfg *config.Config) error {
	if req.GetWorkspaceId() == "" {
		return fmt.Errorf("workspace_id is required")
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
		return fmt.Errorf("start_time and end_time are required")
	}
	if end.Before(start) {
		return fmt.Errorf("end_time must be after start_time")
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
