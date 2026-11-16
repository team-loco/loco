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
	"github.com/team-loco/loco/internal/clickhouse"
)

var (
	errNonCanonicalID = errors.New("IDs must be lowercase UUIDs")
	errSinceTooOld    = errors.New("since is older than the maximum time range")
	errSinceInFuture  = errors.New("since is in the future")
	errTimestampRange = errors.New("timestamp is outside nanosecond precision range")
	errTooManyBuckets = errors.New("time range holds too many buckets")
)

// Limits bounds what one query may ask for.
type Limits struct {
	DefaultLimit int32
	MaxLimit     int32
	MaxTimeRange time.Duration
	MaxBuckets   int32
}

// ValidateLogsRequest validates the query parameters for a logs request.
func ValidateLogsRequest(req *observabilityv1.QueryLogsRequest, limits Limits) error {
	if err := validateCanonicalIDs(req.GetWorkspaceId(), req.GetEnvironmentId(), req.GetResourceIds()); err != nil {
		return err
	}
	return validateQueryTimeRange(req.GetStartTime(), req.GetEndTime(), limits.MaxTimeRange)
}

func ValidateLogBucketsRequest(req *observabilityv1.QueryLogBucketsRequest, limits Limits) error {
	if err := validateCanonicalIDs(req.GetWorkspaceId(), req.GetEnvironmentId(), req.GetResourceIds()); err != nil {
		return err
	}
	if err := validateQueryTimeRange(req.GetStartTime(), req.GetEndTime(), limits.MaxTimeRange); err != nil {
		return err
	}
	buckets := clickhouse.BucketCount(req.GetStartTime().AsTime(), req.GetEndTime().AsTime(), req.GetIntervalSeconds())
	if buckets > int64(limits.MaxBuckets) {
		return fmt.Errorf("%w: %d, at most %d", errTooManyBuckets, buckets, limits.MaxBuckets)
	}
	return nil
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

// ValidateTraceBucketsRequest also bounds the number of buckets the range spans.
func ValidateTraceBucketsRequest(req *observabilityv1.QueryTraceBucketsRequest, limits Limits) error {
	if err := ValidateTraceScope(req.GetScope()); err != nil {
		return err
	}
	if err := validateQueryTimeRange(req.GetStartTime(), req.GetEndTime(), limits.MaxTimeRange); err != nil {
		return err
	}
	buckets := clickhouse.BucketCount(req.GetStartTime().AsTime(), req.GetEndTime().AsTime(), req.GetIntervalSeconds())
	if buckets > int64(limits.MaxBuckets) {
		return fmt.Errorf("%w: %d, at most %d", errTooManyBuckets, buckets, limits.MaxBuckets)
	}
	return nil
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
