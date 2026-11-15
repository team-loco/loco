package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	metricsNamespace     = "platform-metrics"
	nodeMetricsNamespace = "platform-metrics-node"
	tenantNamespace      = "tenant-metrics-fixture"
	metricsRegion        = "test-region"
	metricsInterval      = "3s"
	metricsTimeout       = 3 * time.Minute
	outage               = 8 * time.Second
	sinkPath             = "/var/local/loco-platform-metrics"
	sinkFile             = sinkPath + "/output.json"
	sinkToken            = "local-metrics-fixture" //nolint:gosec // the throwaway sink's bearer token
	sinkName             = "otlp-sink"
	collectorPartOf      = "opentelemetry-collector"
	clusterCollector     = "otel-col-cluster"
	nodeCollector        = "otel-col-daemon-agent"
)

var errNoMetrics = errors.New("not in the sink yet")

// TestClusterMetrics installs the loco-worker chart's node and cluster collectors in platform-only
// mode, exporting to an OTLP sink that takes the chart's ingest token Secret. It checks that real
// cluster and node metrics and platform namespace events arrive, tenant namespaces stay out, and
// what the collectors queued while the sink was down arrives once it returns, after the collector
// pods were replaced.
func TestClusterMetrics(t *testing.T) {
	t.Parallel()
	marker := randomHex(t, 4)
	var chart struct {
		Global struct {
			Exporter struct {
				IngestToken struct {
					SecretName string `json:"secretName"`
					SecretKey  string `json:"secretKey"`
				} `json:"ingestToken"`
			} `json:"exporter"`
		} `json:"global"`
		Daemon struct {
			Image struct {
				Repository string `json:"repository"`
				Tag        string `json:"tag"`
			} `json:"image"`
		} `json:"otel-col-daemon"`
	}
	mustReadYAML(t, c.Path("charts", "loco-worker", "values.yaml"), &chart)
	t.Cleanup(func() {
		if t.Failed() {
			c.LogKubectl(
				t,
				"get pods -A -o wide",
				"-n "+metricsNamespace+" logs statefulset/"+clusterCollector+" --tail=80",
				"-n "+nodeMetricsNamespace+" logs daemonset/"+nodeCollector+" --tail=80",
				"-n "+metricsNamespace+" logs deployment/"+sinkName+" --tail=80",
			)
		}
	})

	c.MustApply(t, namespace(metricsNamespace, "privileged")+"---\n"+namespace(nodeMetricsNamespace, "privileged")+
		"---\n"+namespace(tenantNamespace, "baseline"))
	collectors := collectorManifest(t)
	c.MustKubectl(t, "-n", metricsNamespace, "delete", "deployment", sinkName, "--ignore-not-found", "--wait=true")
	if _, err := c.KubectlInput(
		t.Context(),
		collectors,
		"delete",
		"--ignore-not-found",
		"--wait=true",
		"-f",
		"-",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Command(t.Context(), "docker", "exec", c.Node(), "rm", "-f", sinkFile); err != nil {
		t.Fatal(err)
	}
	installSink(t, chart.Daemon.Image.Repository+":"+chart.Daemon.Image.Tag)
	token := chart.Global.Exporter.IngestToken
	for _, ns := range []string{metricsNamespace, nodeMetricsNamespace} {
		applySecret(t, ns, token.SecretName, map[string]string{token.SecretKey: sinkToken})
	}
	c.MustApply(t, collectors)
	rollout(t, nodeMetricsNamespace, "daemonset/"+nodeCollector)
	rollout(t, metricsNamespace, "statefulset/"+clusterCollector)

	platform.Eventually(t, metricsTimeout, "cluster and node metrics reach the sink", func(ctx context.Context) error {
		names := map[string]bool{}
		for _, point := range readSink(ctx).points() {
			names[point.name] = true
		}
		for _, name := range []string{"k8s.deployment.available", "k8s.node.allocatable_cpu", "k8s.node.cpu.usage"} {
			if !names[name] {
				return fmt.Errorf("%s: %w", name, errNoMetrics)
			}
		}
		return nil
	})
	sink := readSink(t.Context())
	available := false
	for _, point := range sink.points() {
		if point.name == "k8s.deployment.available" && point.attributes["k8s.deployment.name"] == sinkName &&
			point.value == 1 {
			available = true
		}
		if point.attributes["loco.telemetry.scope"] != "platform" ||
			point.attributes["k8s.cluster.name"] != c.KindCluster() || point.attributes["loco.region"] != metricsRegion {
			t.Fatalf("metric %s carries the resource %v", point.name, point.attributes)
		}
	}
	if !available {
		t.Error("no k8s.deployment.available point reports the sink Deployment available")
	}
	sink.checkNoTenant(t, marker)

	event(t, "platform-before-"+marker, metricsNamespace)
	event(t, "tenant-before-"+marker, tenantNamespace)
	waitForEvent(t, "platform-before-"+marker)
	readSink(t.Context()).checkNoTenant(t, marker)

	cutoff := time.Now()
	c.MustKubectl(t, "-n", metricsNamespace, "scale", "deployment/"+sinkName, "--replicas=0")
	c.MustKubectl(t, "-n", metricsNamespace, "wait", "--for=delete", "pod", "-l", "app="+sinkName,
		"--timeout="+metricsTimeout.String())
	event(t, "platform-queued-"+marker, metricsNamespace)
	time.Sleep(outage)
	c.MustKubectl(
		t,
		"-n",
		metricsNamespace,
		"delete",
		"pod",
		"-l",
		"app.kubernetes.io/name="+clusterCollector,
		"--wait=true",
	)
	c.MustKubectl(t, "-n", nodeMetricsNamespace, "delete", "pod", "-l", "app.kubernetes.io/name=otel-col-daemon",
		"--wait=true")
	rollout(t, metricsNamespace, "statefulset/"+clusterCollector)
	rollout(t, nodeMetricsNamespace, "daemonset/"+nodeCollector)
	recovery := time.Now()
	c.MustKubectl(t, "-n", metricsNamespace, "scale", "deployment/"+sinkName, "--replicas=1")
	rollout(t, metricsNamespace, "deployment/"+sinkName)

	waitForEvent(t, "platform-queued-"+marker)
	platform.Eventually(t, metricsTimeout, "metrics from the outage replay with their timestamps",
		func(ctx context.Context) error {
			for _, point := range readSink(ctx).points() {
				if point.time.After(cutoff) && point.time.Before(recovery) {
					return nil
				}
			}
			return errNoMetrics
		})
	readSink(t.Context()).checkNoTenant(t, marker)
}

func namespace(name, podSecurity string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    pod-security.kubernetes.io/enforce: %s
`, name, podSecurity)
}

// collectorManifest renders the chart's collectors, platform-only, exporting to the sink.
func collectorManifest(t *testing.T) string {
	t.Helper()
	values := writeFile(t, "metrics-values.yaml", fmt.Sprintf(`global:
  region: %[1]s
  platformTelemetry:
    enabled: true
    platformOnly: true
    installation: metrics-e2e
    cluster: %[2]s
    environment: test
    collectionInterval: %[3]s
    namespaces: []
  exporter:
    endpoint: http://%[4]s.%[5]s:4318
    ingestToken:
      enabled: true
agent:
  image:
    tag: unused
controller:
  image:
    tag: unused
cluster:
  role: apps
certManager:
  existingTLSSecret: unused
gateway-helm:
  enabled: false
otel-col-deploy:
  enabled: false
otel-col-cluster:
  namespaceOverride: %[5]s
otel-col-daemon:
  namespaceOverride: %[6]s
`, metricsRegion, c.KindCluster(), metricsInterval, sinkName, metricsNamespace, nodeMetricsNamespace))
	rendered := helm(t, "template", "metrics", c.Path("charts", "loco-worker"), "--namespace", metricsNamespace,
		"--values", values)
	var collectors []any
	for _, document := range renderedDocuments(t, rendered) {
		var object struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		}
		roundTrip(t, document, &object)
		if object.Metadata.Labels["app.kubernetes.io/part-of"] == collectorPartOf {
			collectors = append(collectors, document)
		}
	}
	list, err := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": collectors})
	if err != nil {
		t.Fatal(err)
	}
	return string(list)
}

func installSink(t *testing.T, image string) {
	t.Helper()
	c.MustApply(t, fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %[1]s
  namespace: %[2]s
data:
  config.yaml: |
    receivers:
      otlp:
        protocols:
          http:
            endpoint: 0.0.0.0:4318
            auth:
              authenticator: bearertokenauth
    extensions:
      bearertokenauth:
        token: %[3]s
    exporters:
      file:
        path: /data/output.json
        flush_interval: 100ms
    service:
      extensions: [bearertokenauth]
      pipelines:
        metrics:
          receivers: [otlp]
          exporters: [file]
        logs:
          receivers: [otlp]
          exporters: [file]
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %[1]s
  template:
    metadata:
      labels:
        app: %[1]s
    spec:
      containers:
        - name: sink
          image: %[4]s
          args: [--config=/conf/config.yaml]
          securityContext:
            runAsUser: 0
          volumeMounts:
            - name: config
              mountPath: /conf
            - name: output
              mountPath: /data
      volumes:
        - name: config
          configMap:
            name: %[1]s
        - name: output
          hostPath:
            path: %[5]s
            type: DirectoryOrCreate
---
apiVersion: v1
kind: Service
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  selector:
    app: %[1]s
  ports:
    - port: 4318
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-test-otlp
  namespace: %[2]s
spec:
  podSelector:
    matchLabels:
      app: %[1]s
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: %[2]s
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: %[6]s
      ports:
        - port: 4318
          protocol: TCP
`, sinkName, metricsNamespace, sinkToken, image, sinkPath, nodeMetricsNamespace))
	rollout(t, metricsNamespace, "deployment/"+sinkName)
}

func rollout(t *testing.T, ns, workload string) {
	t.Helper()
	c.MustKubectl(t, "-n", ns, "rollout", "status", workload, "--timeout="+metricsTimeout.String())
}

func event(t *testing.T, name, ns string) {
	t.Helper()
	c.MustApply(t, fmt.Sprintf(`apiVersion: v1
kind: Event
metadata:
  name: %[1]s
  namespace: %[2]s
involvedObject:
  apiVersion: v1
  kind: Pod
  name: metrics-fixture
  namespace: %[2]s
reason: MetricsFixture
message: %[1]s
type: Normal
source:
  component: metrics-e2e
`, name, ns))
}

func waitForEvent(t *testing.T, message string) {
	t.Helper()
	platform.Eventually(t, metricsTimeout, "event "+message+" reaches the sink", func(ctx context.Context) error {
		if !strings.Contains(strings.Join(readSink(ctx).logs, "\n"), message) {
			return errNoMetrics
		}
		return nil
	})
}

// sinkOutput is what the sink's file exporter wrote, one OTLP/JSON export per line.
type sinkOutput struct {
	raw     string
	metrics []pmetric.Metrics
	logs    []string
}

type metricPoint struct {
	name       string
	attributes map[string]any
	value      float64
	time       time.Time
}

// readSink reads the sink's file from the node, as far as it is written.
func readSink(ctx context.Context) sinkOutput {
	// The file is missing until the sink first writes.
	raw, err := c.Command(ctx, "docker", "exec", c.Node(), "cat", sinkFile)
	if err != nil {
		raw = ""
	}
	output := sinkOutput{raw: raw}
	var unmarshaler pmetric.JSONUnmarshaler
	for line := range strings.Lines(raw) {
		switch {
		case strings.Contains(line, `"resourceLogs"`):
			output.logs = append(output.logs, line)
		case strings.Contains(line, `"resourceMetrics"`):
			if metrics, err := unmarshaler.UnmarshalMetrics([]byte(line)); err == nil {
				output.metrics = append(output.metrics, metrics)
			}
		}
	}
	return output
}

func (s sinkOutput) points() []metricPoint {
	var points []metricPoint
	for _, metrics := range s.metrics {
		for _, resource := range metrics.ResourceMetrics().All() {
			attributes := resource.Resource().Attributes().AsRaw()
			for _, scope := range resource.ScopeMetrics().All() {
				for _, metric := range scope.Metrics().All() {
					add := func(value float64, timestamp pcommon.Timestamp) {
						points = append(points, metricPoint{metric.Name(), attributes, value, timestamp.AsTime()})
					}
					numbers := pmetric.NewNumberDataPointSlice()
					switch metric.Type() {
					case pmetric.MetricTypeGauge:
						numbers = metric.Gauge().DataPoints()
					case pmetric.MetricTypeSum:
						numbers = metric.Sum().DataPoints()
					case pmetric.MetricTypeHistogram:
						for _, point := range metric.Histogram().DataPoints().All() {
							add(0, point.Timestamp())
						}
					default:
					}
					for _, point := range numbers.All() {
						value := point.DoubleValue()
						if point.ValueType() == pmetric.NumberDataPointValueTypeInt {
							value = float64(point.IntValue())
						}
						add(value, point.Timestamp())
					}
				}
			}
		}
	}
	return points
}

// checkNoTenant fails the test when a tenant namespace's metric or event reached the sink.
func (s sinkOutput) checkNoTenant(t *testing.T, marker string) {
	t.Helper()
	if strings.Contains(s.raw, "tenant-before-"+marker) {
		t.Error("the tenant namespace's event reached the sink")
	}
	for _, point := range s.points() {
		if point.attributes["k8s.namespace.name"] == tenantNamespace {
			t.Errorf("metric %s of the tenant namespace reached the sink", point.name)
		}
	}
}
