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
	"github.com/team-loco/loco/internal/systemtenant"
)

const (
	organizationAttribute  = "loco.organization.id"
	workspaceAttribute     = "loco.workspace.id"
	environmentAttribute   = "loco.environment.id"
	resourceAttribute      = "loco.resource.id"
	clusterAttribute       = "loco.cluster.id"
	retentionAttribute     = "loco.retention.days"
	rejectionMessagePrefix = "rejected records this cluster may not write: "
	rejectionSeparator     = ", "
	rejectionCountSep      = "="

	rejectedRecordsMetric = "loco_ingest_rejected_records_total"
	rejectedRecordsHelp   = "Telemetry records dropped because the sending cluster may not write them to their tenant."
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
	workload  bool
}

func newTenancyFilter(ctx context.Context, admission *Admission) *tenancyFilter {
	return &tenancyFilter{ctx: ctx, admission: admission, counts: map[verdict]int64{}}
}

func (f *tenancyFilter) reject(resource pcommon.Resource, timestamp pcommon.Timestamp) bool {
	if f.err != nil || f.admission.system(resource) {
		return false
	}
	if platformScope(resource) {
		f.counts[rejectPlatformTenant]++
		return true
	}
	result, err := f.admission.check(f.ctx, workspaceOf(resource), timestamp.AsTime())
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

// stamp sets the cluster and the days to keep each resource's records of one signal, replacing
// any values the client sent, and moves system records into the system tenant.
func (f *tenancyFilter) stamp(resources []pcommon.Resource, signalDays func(*ingestv1.Retention) int32) {
	if f.err != nil {
		return
	}
	var placed []string
	for _, resource := range resources {
		if f.admission.system(resource) {
			continue
		}
		if workspace := workspaceOf(resource); workspace != "" && f.admission.placed(workspace) {
			placed = append(placed, workspace)
		}
	}
	gate := f.admission.gate
	retention, err := gate.retention.lookup(f.ctx, placed, gate.fetchRetention(f.admission.token))
	if err != nil {
		f.err = err
		return
	}
	for _, resource := range resources {
		attrs := resource.Attributes()
		attrs.PutStr(clusterAttribute, f.admission.ClusterID)
		if f.admission.system(resource) {
			stampSystem(attrs)
			attrs.PutStr(retentionAttribute, strconv.Itoa(int(gate.cfg.SystemRetentionDays)))
			continue
		}
		f.workload = true
		days := f.admission.placements.retention
		if workspace := workspaceOf(resource); workspace != "" {
			days = retention[workspace]
		}
		if days == nil {
			attrs.Remove(retentionAttribute)
			continue
		}
		attrs.PutStr(retentionAttribute, strconv.Itoa(int(signalDays(days))))
	}
}

func stampSystem(attrs pcommon.Map) {
	for _, key := range []string{organizationAttribute, environmentAttribute, resourceAttribute} {
		attrs.Remove(key)
	}
	attrs.PutStr(workspaceAttribute, systemtenant.WorkspaceID)
	attrs.PutStr(systemtenant.ScopeAttribute, systemtenant.PlatformScope)
}

func platformScope(resource pcommon.Resource) bool {
	value, _ := resource.Attributes().Get(systemtenant.ScopeAttribute)
	return value.Str() == systemtenant.PlatformScope
}

func workspaceOf(resource pcommon.Resource) string {
	value, _ := resource.Attributes().Get(workspaceAttribute)
	return value.Str()
}

func (f *tenancyFilter) logs(logs plog.Logs) {
	all := logs.ResourceLogs()
	all.RemoveIf(func(resourceLogs plog.ResourceLogs) bool {
		resource := resourceLogs.Resource()
		resourceLogs.ScopeLogs().RemoveIf(func(scopeLogs plog.ScopeLogs) bool {
			scopeLogs.LogRecords().RemoveIf(func(record plog.LogRecord) bool {
				timestamp := record.Timestamp()
				if timestamp == 0 {
					timestamp = record.ObservedTimestamp()
				}
				return f.reject(resource, timestamp)
			})
			return scopeLogs.LogRecords().Len() == 0
		})
		return resourceLogs.ScopeLogs().Len() == 0
	})
	resources := make([]pcommon.Resource, 0, all.Len())
	for i := range all.Len() {
		resources = append(resources, all.At(i).Resource())
	}
	f.stamp(resources, (*ingestv1.Retention).GetLogDays)
}

func (f *tenancyFilter) traces(traces ptrace.Traces) {
	all := traces.ResourceSpans()
	all.RemoveIf(func(resourceSpans ptrace.ResourceSpans) bool {
		resource := resourceSpans.Resource()
		resourceSpans.ScopeSpans().RemoveIf(func(scopeSpans ptrace.ScopeSpans) bool {
			scopeSpans.Spans().RemoveIf(func(span ptrace.Span) bool {
				return f.reject(resource, span.StartTimestamp())
			})
			return scopeSpans.Spans().Len() == 0
		})
		return resourceSpans.ScopeSpans().Len() == 0
	})
	resources := make([]pcommon.Resource, 0, all.Len())
	for i := range all.Len() {
		resources = append(resources, all.At(i).Resource())
	}
	f.stamp(resources, (*ingestv1.Retention).GetTraceDays)
}

func (f *tenancyFilter) metrics(metrics pmetric.Metrics) {
	all := metrics.ResourceMetrics()
	all.RemoveIf(func(resourceMetrics pmetric.ResourceMetrics) bool {
		resource := resourceMetrics.Resource()
		resourceMetrics.ScopeMetrics().RemoveIf(func(scopeMetrics pmetric.ScopeMetrics) bool {
			scopeMetrics.Metrics().RemoveIf(func(metric pmetric.Metric) bool {
				return f.metricEmptied(resource, metric)
			})
			return scopeMetrics.Metrics().Len() == 0
		})
		return resourceMetrics.ScopeMetrics().Len() == 0
	})
	resources := make([]pcommon.Resource, 0, all.Len())
	for i := range all.Len() {
		resources = append(resources, all.At(i).Resource())
	}
	f.stamp(resources, (*ingestv1.Retention).GetMetricDays)
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
