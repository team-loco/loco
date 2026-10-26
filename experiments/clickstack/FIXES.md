# ClickStack Setup Fixes

This document describes the critical fixes applied to make the ClickStack + OpenTelemetry Demo setup work correctly.

## Issue 1: Wrong OTEL Collector Endpoint

**Problem:** The ClickHouse OpenTelemetry Demo manifest hardcodes `OTEL_COLLECTOR_NAME=my-hyperdx-hdx-oss-v2-otel-collector`, but ClickStack deploys the service as `clickstack-otel-collector`.

**Symptom:** No telemetry data flowing to ClickHouse. Demo apps couldn't reach the collector.

**Fix:**
```bash
kubectl set env deployment -n otel-demo -l app.kubernetes.io/part-of=opentelemetry-demo \
  OTEL_COLLECTOR_NAME=clickstack-otel-collector
```

**Applied in:** [setup.sh:54](experiments/clickstack/setup.sh#L54)

---

## Issue 2: Load Generator Image Pull Failures

**Problem:** The ClickHouse demo uses `clickhouse/ch-otel-demo:latest-load-generator` which:
1. Hits Docker Hub rate limits (429 Too Many Requests)
2. Doesn't support ARM64/multi-platform

**Symptom:** Load generator pod stuck in `ImagePullBackOff`, minimal telemetry generated.

**Fix:**
```bash
kubectl set image deployment/load-generator -n otel-demo \
  load-generator=ghcr.io/open-telemetry/demo:2.1.3-load-generator
```

**Applied in:** [setup.sh:57](experiments/clickstack/setup.sh#L57)

**Result:** Load generator now works, generating ~559 traces/minute (up from 2/minute).

---

## Issue 3: Missing Kubernetes Metrics

**Problem:** HyperDX Kubernetes dashboard shows no data. ClickStack's OTEL collector is managed by OpAMP (dynamic config) and doesn't collect K8s infrastructure metrics.

**Symptom:** Empty K8s dashboard, queries for `k8s.container.*`, `k8s.pod.*`, `k8s.node.*` metrics return no results.

**Fix:** Deploy separate DaemonSet-based OTEL collector with `kubeletstats` receiver.

**Applied in:**
- [k8s-metrics-collector.yaml](experiments/clickstack/k8s-metrics-collector.yaml) - Full manifest
- [setup.sh:60](experiments/clickstack/setup.sh#L60) - Auto-deployment

**Components:**
- ServiceAccount + RBAC for cluster access
- ConfigMap with kubeletstats receiver config
- DaemonSet (runs on each node) to collect metrics
- Exports to ClickStack OTEL collector via OTLP

**Metrics collected:**
- `k8s.node.*` - CPU, memory, filesystem, network
- `k8s.pod.*` - CPU, memory, network, filesystem
- `k8s.container.*` - CPU/memory utilization

---

## Why These Fixes Are Needed

### ClickHouse Demo vs Official Demo
The ClickHouse fork of the OpenTelemetry demo adds HyperDX integration but:
- Uses custom Docker images not available on all platforms
- Hardcodes collector endpoint names that don't match ClickStack

### ClickStack OpAMP Limitation
ClickStack uses OpAMP (Open Agent Management Protocol) to manage OTEL collector config dynamically. This means:
- `customConfig` in values.yaml gets overridden
- Can't add receivers via Helm values
- Need separate collector for infrastructure metrics

### Docker Hub Rate Limiting
Anonymous Docker Hub pulls are limited to 100 per 6 hours. The ClickHouse images trigger this quickly in testing.

---

## Verification

Check everything is working:

```bash
# Check telemetry in ClickHouse
kubectl exec -n otel-demo clickstack-clickhouse-<pod> -- clickhouse-client -q \
  "SELECT COUNT(*) FROM default.otel_traces"

# Check K8s metrics
kubectl exec -n otel-demo clickstack-clickhouse-<pod> -- clickhouse-client -q \
  "SELECT DISTINCT MetricName FROM default.otel_metrics_gauge WHERE MetricName LIKE '%k8s%' LIMIT 10"

# Check load generator
kubectl get pods -n otel-demo | grep load-generator
```

All three fixes are automatically applied by [setup.sh](experiments/clickstack/setup.sh).
