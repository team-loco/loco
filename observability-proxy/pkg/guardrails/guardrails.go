package guardrails

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

var (
	errInvalidEnvironmentID = errors.New("environment_id must be a UUID")
	errInvalidRegionFilter  = errors.New("region filters must be nonempty with at most one trailing wildcard")
	errMetricNameRequired   = errors.New("metric_name is required")
	errTimeRangeRequired    = errors.New("start_time and end_time are required")
	errEndBeforeStart       = errors.New("end_time must be after start_time")
	errWorkspaceIDRequired  = errors.New("workspace_id is required")
	errSinceTooOld          = errors.New("since is older than the maximum time range")
	errSinceInFuture        = errors.New("since is in the future")
	errInvalidTraceID       = errors.New("trace_id must be 32 lowercase hex characters and nonzero")
	errInvalidTraceLimit    = errors.New("limit must be between 0 and 10000")
	errTimestampRange       = errors.New("timestamp is outside nanosecond precision range")
	errReservedLabelKey     = errors.New("label keys in the loco. attribute namespace are reserved")
)

const (
	reservedAttributePrefix = "loco."
	kubernetesLabelPrefix   = "loco.io/"
	maxRegionPatterns       = 100
	maxRegionPatternRunes   = 128
	traceIDBytes            = 16
	traceRequestMaxLimit    = 10000
)

// ValidateLogsRequest validates and clamps the query parameters for a logs request.
func ValidateLogsRequest(req *observabilityv1.QueryLogsRequest, cfg *config.Config) error {
	if req.GetWorkspaceId() == "" {
		return errWorkspaceIDRequired
	}
	if err := validateEnvironmentID(req.GetEnvironmentId()); err != nil {
		return err
	}
	if err := validateLabels(req.GetLabels()); err != nil {
		return err
	}
	if err := validateRegions(req.GetRegions(), req.GetExcludedRegions()); err != nil {
		return err
	}

	return validateQueryTimeRange(req.GetStartTime(), req.GetEndTime(), cfg.MaxTimeRange)
}

// ValidateMetricsRequest validates and clamps the query parameters for a metrics request.
func ValidateMetricsRequest(req *observabilityv1.QueryMetricsRequest, cfg *config.Config) error {
	if req.GetWorkspaceId() == "" {
		return errWorkspaceIDRequired
	}
	if req.GetMetricName() == "" {
		return errMetricNameRequired
	}

	if err := validateQueryTimeRange(req.GetStartTime(), req.GetEndTime(), cfg.MaxTimeRange); err != nil {
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
	if err := validateEnvironmentID(req.GetEnvironmentId()); err != nil {
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

func ValidateTracesRequest(req *observabilityv1.QueryTracesRequest, cfg *config.Config) error {
	if req.GetWorkspaceId() == "" {
		return errWorkspaceIDRequired
	}
	if err := uuid.Validate(req.GetWorkspaceId()); err != nil {
		return err
	}
	for _, id := range req.GetResourceIds() {
		if err := uuid.Validate(id); err != nil {
			return err
		}
	}
	if req.GetEnvironmentId() != "" {
		if err := uuid.Validate(req.GetEnvironmentId()); err != nil {
			return err
		}
	}
	if traceID := req.GetTraceId(); traceID != "" {
		decoded, err := hex.DecodeString(traceID)
		if err != nil || len(decoded) != traceIDBytes || traceID != strings.ToLower(traceID) ||
			traceID == strings.Repeat("0", traceIDBytes*2) {
			return errInvalidTraceID
		}
	}
	if req.GetLimit() < 0 || req.GetLimit() > traceRequestMaxLimit {
		return errInvalidTraceLimit
	}
	return validateQueryTimeRange(req.GetStartTime(), req.GetEndTime(), cfg.MaxTimeRange)
}

func validateQueryTimeRange(startTimestamp, endTimestamp *timestamppb.Timestamp, maxRange time.Duration) error {
	if startTimestamp == nil || endTimestamp == nil {
		return errTimeRangeRequired
	}
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
	return validateTimeRange(start, end, maxRange)
}

func validateRegions(groups ...[]string) error {
	for _, group := range groups {
		if len(group) > maxRegionPatterns {
			return errInvalidRegionFilter
		}
		for _, value := range group {
			if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > maxRegionPatternRunes ||
				strings.Contains(strings.TrimSuffix(value, "*"), "*") {
				return errInvalidRegionFilter
			}
		}
	}
	return nil
}

func validateEnvironmentID(value string) error {
	if value == "" {
		return nil
	}
	if _, err := uuid.Parse(value); err != nil {
		return errInvalidEnvironmentID
	}
	return nil
}
