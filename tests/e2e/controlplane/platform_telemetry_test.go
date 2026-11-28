package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/internal/systemtenant"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func (cp *controlPlane) checkPlatformCollection(t *testing.T, reader, password, clickhousePod string) {
	t.Helper()
	query := func(ctx context.Context, statement string) (string, error) {
		return c.Kubectl(ctx, "-n", cp.namespace, "exec", clickhousePod, "-c", "clickhouse", "--", "clickhouse-client",
			"--user", reader, "--password", password, "-q", statement)
	}
	check := func(phase string) {
		cutoff := time.Now().UnixNano()
		marker := "control-plane-event-" + platform.RandomHex(4)
		event(t, marker, cp.namespace)
		for _, metric := range []string{"k8s.node.cpu.usage", "k8s.node.allocatable_cpu", "k8s.deployment.available"} {
			var receipt string
			platform.Eventually(t, metricsTimeout, phase+": fresh "+metric+" reaches system storage", func(ctx context.Context) error {
				statement := fmt.Sprintf(`SELECT MetricName, Value,
ResourceAttributes['k8s.node.name'] AS node,
ResourceAttributes['loco.cluster.id'] AS cluster,
ResourceAttributes['loco.region'] AS region FROM
(SELECT MetricName, Value, ResourceAttributes, WorkspaceId, TimeUnix FROM loco_obs.otel_metrics_gauge
 UNION ALL SELECT MetricName, Value, ResourceAttributes, WorkspaceId, TimeUnix FROM loco_obs.otel_metrics_sum)
WHERE WorkspaceId = '%s' AND MetricName = '%s'
AND ResourceAttributes['loco.cluster.id'] = 'control-plane'
AND ResourceAttributes['loco.region'] = '%s'
AND TimeUnix >= fromUnixTimestamp64Nano(%d) LIMIT 1 FORMAT JSONEachRow`,
					systemtenant.WorkspaceID, metric, cp.values.PlatformTelemetry.Region, cutoff)
				row, err := query(ctx, statement)
				if err != nil {
					return err
				}
				var observed struct {
					Name string `json:"MetricName"`
					Node string `json:"node"`
				}
				if err := json.Unmarshal([]byte(row), &observed); err != nil {
					return fmt.Errorf("no fresh %s record: %w", metric, err)
				}
				if observed.Name != metric {
					return fmt.Errorf("got metric %q, want %q", observed.Name, metric)
				}
				if strings.HasPrefix(metric, "k8s.node.") && observed.Node == "" {
					return fmt.Errorf("%s lacks node identity", metric)
				}
				receipt = strings.TrimSpace(row)
				return nil
			})
			t.Logf("synthetic installation: %s; stored infrastructure metric: %s", phase, receipt)
		}
		var receipt string
		platform.Eventually(t, metricsTimeout, phase+": Kubernetes event reaches system storage", func(ctx context.Context) error {
			row, err := query(ctx, fmt.Sprintf(`SELECT Body,
ResourceAttributes['k8s.namespace.name'] AS namespace,
ResourceAttributes['loco.cluster.id'] AS cluster,
ResourceAttributes['loco.region'] AS region FROM loco_obs.otel_logs
WHERE WorkspaceId = '%s' AND position(Body, '%s') > 0
AND ResourceAttributes['k8s.namespace.name'] = '%s'
AND ResourceAttributes['loco.cluster.id'] = 'control-plane'
AND ResourceAttributes['loco.region'] = '%s'
AND Timestamp >= fromUnixTimestamp64Nano(%d) LIMIT 1 FORMAT JSONEachRow`,
				systemtenant.WorkspaceID, marker, cp.namespace, cp.values.PlatformTelemetry.Region, cutoff))
			if err != nil {
				return err
			}
			receipt = strings.TrimSpace(row)
			if !json.Valid([]byte(receipt)) {
				return fmt.Errorf("event %s has not reached system storage", marker)
			}
			return nil
		})
		t.Logf("synthetic installation: %s; created event %s; stored event: %s", phase, marker, receipt)
	}
	check("platform telemetry enabled")
	cp.upgrade(t, "platformTelemetry.logs.enabled=false")
	t.Cleanup(func() { cp.upgrade(t) })
	c.MustKubectl(t, "-n", cp.values.NodeCollector.NamespaceOverride, "get", "daemonset/node-collector-agent")
	c.MustKubectl(t, "-n", cp.values.ClusterCollector.NamespaceOverride, "get", "statefulset/cluster-collector")
	check("platform stdout disabled")
}
