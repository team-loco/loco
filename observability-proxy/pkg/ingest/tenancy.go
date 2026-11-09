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

	"github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
)

const (
	workspaceAttribute     = "loco.workspace.id"
	resourceAttribute      = "loco.resource.id"
	environmentAttribute   = "loco.environment.id"
	clusterAttribute       = "loco.cluster.id"
	rejectionMessagePrefix = "rejected records outside a placement on this cluster: "
	rejectionSeparator     = ", "
	rejectionCountSep      = "="

	rejectedRecordsMetric = "loco_ingest_rejected_records_total"
	rejectedRecordsHelp   = "Telemetry records dropped because their workspace had no placement on the sending cluster."
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

func (f *tenancyFilter) reject(resource pcommon.Resource, timestamp pcommon.Timestamp) bool {
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
		return false
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

func (f *tenancyFilter) stamp(resource pcommon.Resource) {
	resource.Attributes().PutStr(clusterAttribute, f.admission.ClusterID)
}

func (f *tenancyFilter) logs(logs plog.Logs) {
	logs.ResourceLogs().RemoveIf(func(resourceLogs plog.ResourceLogs) bool {
		resource := resourceLogs.Resource()
		resourceLogs.ScopeLogs().RemoveIf(func(scopeLogs plog.ScopeLogs) bool {
			scopeLogs.LogRecords().RemoveIf(func(record plog.LogRecord) bool {
				return f.reject(resource, logTimestamp(record))
			})
			return scopeLogs.LogRecords().Len() == 0
		})
		if resourceLogs.ScopeLogs().Len() > 0 {
			first := resourceLogs.ScopeLogs().At(0).LogRecords().At(0)
			f.stampEnvironment(resource, logTimestamp(first))
		}
		f.stamp(resource)
		return resourceLogs.ScopeLogs().Len() == 0
	})
}

func logTimestamp(record plog.LogRecord) pcommon.Timestamp {
	if record.Timestamp() == 0 {
		return record.ObservedTimestamp()
	}
	return record.Timestamp()
}

// stampEnvironment names the environment of a resource's Kubernetes events, whose objects carry no
// pod labels, from the resource's placement on the sending cluster.
func (f *tenancyFilter) stampEnvironment(resource pcommon.Resource, timestamp pcommon.Timestamp) {
	if f.err != nil {
		return
	}
	attributes := resource.Attributes()
	scope, _ := attributes.Get(clickhouse.TelemetryScopeAttribute)
	if scope.Str() != clickhouse.KubernetesEventScope {
		return
	}
	resourceID, ok := attributes.Get(resourceAttribute)
	if !ok {
		return
	}
	environmentID, err := f.admission.environment(f.ctx, resourceID.Str(), timestamp.AsTime())
	if err != nil {
		f.err = err
		return
	}
	if environmentID != "" {
		attributes.PutStr(environmentAttribute, environmentID)
	}
}

func (f *tenancyFilter) traces(traces ptrace.Traces) {
	traces.ResourceSpans().RemoveIf(func(resourceSpans ptrace.ResourceSpans) bool {
		resource := resourceSpans.Resource()
		resourceSpans.ScopeSpans().RemoveIf(func(scopeSpans ptrace.ScopeSpans) bool {
			scopeSpans.Spans().RemoveIf(func(span ptrace.Span) bool {
				return f.reject(resource, span.StartTimestamp())
			})
			return scopeSpans.Spans().Len() == 0
		})
		f.stamp(resource)
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
		f.stamp(resource)
		return resourceMetrics.ScopeMetrics().Len() == 0
	})
}

func (f *tenancyFilter) metricEmptied(resource pcommon.Resource, metric pmetric.Metric) bool {
	switch metric.Type() {
	case pmetric.MetricTypeGauge:
		points := metric.Gauge().DataPoints()
		points.RemoveIf(func(point pmetric.NumberDataPoint) bool { return f.reject(resource, point.Timestamp()) })
		return points.Len() == 0
	case pmetric.MetricTypeSum:
		points := metric.Sum().DataPoints()
		points.RemoveIf(func(point pmetric.NumberDataPoint) bool { return f.reject(resource, point.Timestamp()) })
		return points.Len() == 0
	case pmetric.MetricTypeHistogram:
		points := metric.Histogram().DataPoints()
		points.RemoveIf(func(point pmetric.HistogramDataPoint) bool { return f.reject(resource, point.Timestamp()) })
		return points.Len() == 0
	case pmetric.MetricTypeExponentialHistogram:
		points := metric.ExponentialHistogram().DataPoints()
		points.RemoveIf(func(point pmetric.ExponentialHistogramDataPoint) bool {
			return f.reject(resource, point.Timestamp())
		})
		return points.Len() == 0
	case pmetric.MetricTypeSummary:
		points := metric.Summary().DataPoints()
		points.RemoveIf(func(point pmetric.SummaryDataPoint) bool { return f.reject(resource, point.Timestamp()) })
		return points.Len() == 0
	case pmetric.MetricTypeEmpty:
		return false
	default:
		return false
	}
}
