package ingest

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/internal/observabilityconfig"
)

const (
	resourceAttribute      = "loco.resource.id"
	environmentAttribute   = "loco.environment.id"
	buildAttribute         = "loco.io/build-id"
	workspaceAttribute     = "loco.workspace.id"
	clusterAttribute       = "loco.cluster.id"
	retentionAttribute     = "loco.retention.days"
	rejectionMessagePrefix = "rejected telemetry records: "
	rejectionSeparator     = ", "
	rejectionCountSep      = "="

	rejectedRecordsMetric = "loco_ingest_rejected_records_total"
	rejectedRecordsHelp   = "Telemetry records dropped by placement authorization or resource collection settings."
	clusterLabel          = "cluster"
	reasonLabel           = "reason"
)

func newRejectedRecords(registerer prometheus.Registerer) *prometheus.CounterVec {
	counter := prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: rejectedRecordsMetric, Help: rejectedRecordsHelp},
		[]string{clusterLabel, reasonLabel},
	)
	registerer.MustRegister(counter)
	return counter
}

type tenancyFilter struct {
	ctx       context.Context
	admission *Admission
	counts    map[verdict]int64
	err       error
}

func newTenancyFilter(ctx context.Context, admission *Admission) *tenancyFilter {
	return &tenancyFilter{ctx: ctx, admission: admission, counts: map[verdict]int64{}}
}

func (f *tenancyFilter) reject(
	resource pcommon.Resource,
	timestamp pcommon.Timestamp,
	enabled func(observabilityconfig.Config) bool,
) bool {
	if f.err != nil {
		return false
	}
	var workspaceID string
	if value, ok := resource.Attributes().Get(workspaceAttribute); ok {
		workspaceID = value.Str()
	}
	result, err := f.admission.check(f.ctx, workspaceID, timestamp.AsTime())
	if err != nil {
		f.err = err
		return false
	}
	if result == accepted {
		attrs := resource.Attributes()
		resourceID, _ := attrs.Get(resourceAttribute)
		buildID, _ := attrs.Get(buildAttribute)
		if buildID.Str() != "" {
			return false
		}
		environmentID, _ := attrs.Get(environmentAttribute)
		if resourceID.Str() == "" {
			return false
		}
		for _, policy := range slices.Backward(f.admission.placements.windows) {
			if policy.resourceID != resourceID.Str() || policy.workspaceID != workspaceID {
				continue
			}
			if environmentID.Str() != "" && environmentID.Str() != policy.environmentID {
				continue
			}
			at := timestamp.AsTime()
			skew := f.admission.gate.cfg.ClockSkew
			if at.Before(policy.placedAt.Add(-skew)) ||
				(!policy.removedAt.IsZero() && !at.Before(policy.removedAt.Add(skew))) {
				continue
			}
			if enabled(policy.collection) {
				return false
			}
			result = rejectCollectionDisabled
			break
		}
		if result == accepted {
			return false
		}
	}
	f.counts[result]++
	return true
}

func (f *tenancyFilter) rejected() int64 {
	var total int64
	for _, count := range f.counts {
		total += count
	}
	return total
}

func (f *tenancyFilter) message() string {
	reasons := slices.Sorted(maps.Keys(f.counts))
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		count := strconv.FormatInt(f.counts[reason], 10)
		parts = append(parts, string(reason)+rejectionCountSep+count)
	}
	return rejectionMessagePrefix + strings.Join(parts, rejectionSeparator)
}

func (f *tenancyFilter) record(counter *prometheus.CounterVec) {
	for reason, count := range f.counts {
		counter.WithLabelValues(f.admission.ClusterID, string(reason)).Add(float64(count))
	}
}

// stamp sets the cluster and the days to keep the resource's records of one signal, replacing
// any values the client sent.
func (f *tenancyFilter) stamp(resource pcommon.Resource, signalDays func(*ingestv1.Retention) int32) {
	attrs := resource.Attributes()
	attrs.PutStr(clusterAttribute, f.admission.ClusterID)
	workspace, _ := attrs.Get(workspaceAttribute)
	days, ok := f.admission.retention(workspace.Str())
	if !ok {
		attrs.Remove(retentionAttribute)
		return
	}
	attrs.PutStr(retentionAttribute, strconv.Itoa(int(signalDays(days))))
}

func (f *tenancyFilter) logs(logs plog.Logs) {
	logs.ResourceLogs().RemoveIf(func(resourceLogs plog.ResourceLogs) bool {
		resource := resourceLogs.Resource()
		resourceLogs.ScopeLogs().RemoveIf(func(scopeLogs plog.ScopeLogs) bool {
			scopeLogs.LogRecords().RemoveIf(func(record plog.LogRecord) bool {
				timestamp := record.Timestamp()
				if timestamp == 0 {
					timestamp = record.ObservedTimestamp()
				}
				return f.reject(resource, timestamp, logsEnabled)
			})
			return scopeLogs.LogRecords().Len() == 0
		})
		f.stamp(resource, (*ingestv1.Retention).GetLogDays)
		return resourceLogs.ScopeLogs().Len() == 0
	})
}

func (f *tenancyFilter) traces(traces ptrace.Traces) {
	traces.ResourceSpans().RemoveIf(func(resourceSpans ptrace.ResourceSpans) bool {
		resource := resourceSpans.Resource()
		resourceSpans.ScopeSpans().RemoveIf(func(scopeSpans ptrace.ScopeSpans) bool {
			scopeSpans.Spans().RemoveIf(func(span ptrace.Span) bool {
				return f.reject(resource, span.StartTimestamp(), tracesEnabled)
			})
			return scopeSpans.Spans().Len() == 0
		})
		f.stamp(resource, (*ingestv1.Retention).GetTraceDays)
		return resourceSpans.ScopeSpans().Len() == 0
	})
}

func (f *tenancyFilter) metrics(metrics pmetric.Metrics) {
	metrics.ResourceMetrics().RemoveIf(func(resourceMetrics pmetric.ResourceMetrics) bool {
		resource := resourceMetrics.Resource()
		resourceMetrics.ScopeMetrics().RemoveIf(func(scopeMetrics pmetric.ScopeMetrics) bool {
			scopeMetrics.Metrics().RemoveIf(func(metric pmetric.Metric) bool {
				return f.metricEmptied(resource, metric)
			})
			return scopeMetrics.Metrics().Len() == 0
		})
		f.stamp(resource, (*ingestv1.Retention).GetMetricDays)
		return resourceMetrics.ScopeMetrics().Len() == 0
	})
}

func (f *tenancyFilter) metricEmptied(resource pcommon.Resource, metric pmetric.Metric) bool {
	switch metric.Type() {
	case pmetric.MetricTypeGauge:
		points := metric.Gauge().DataPoints()
		points.RemoveIf(
			func(point pmetric.NumberDataPoint) bool { return f.reject(resource, point.Timestamp(), metricsEnabled) },
		)
		return points.Len() == 0
	case pmetric.MetricTypeSum:
		points := metric.Sum().DataPoints()
		points.RemoveIf(
			func(point pmetric.NumberDataPoint) bool { return f.reject(resource, point.Timestamp(), metricsEnabled) },
		)
		return points.Len() == 0
	case pmetric.MetricTypeHistogram:
		points := metric.Histogram().DataPoints()
		points.RemoveIf(func(point pmetric.HistogramDataPoint) bool {
			return f.reject(resource, point.Timestamp(), metricsEnabled)
		})
		return points.Len() == 0
	case pmetric.MetricTypeExponentialHistogram:
		points := metric.ExponentialHistogram().DataPoints()
		points.RemoveIf(func(point pmetric.ExponentialHistogramDataPoint) bool {
			return f.reject(resource, point.Timestamp(), metricsEnabled)
		})
		return points.Len() == 0
	case pmetric.MetricTypeSummary:
		points := metric.Summary().DataPoints()
		points.RemoveIf(func(point pmetric.SummaryDataPoint) bool {
			return f.reject(resource, point.Timestamp(), metricsEnabled)
		})
		return points.Len() == 0
	case pmetric.MetricTypeEmpty:
		return false
	default:
		return false
	}
}

func logsEnabled(cfg observabilityconfig.Config) bool    { return cfg.Logs }
func tracesEnabled(cfg observabilityconfig.Config) bool  { return cfg.Traces }
func metricsEnabled(cfg observabilityconfig.Config) bool { return cfg.Metrics }
