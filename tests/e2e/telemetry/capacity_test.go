package telemetry

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/team-loco/loco/internal/capacitymetrics"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/systemtenant"
	"github.com/team-loco/loco/tests/e2e/platform"
	corev1 "k8s.io/api/core/v1"
)

func TestEffectivePodRequestsReachClickHouse(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	dir := t.TempDir()
	const resourceName = "capacity-proof"
	platform.WriteLocoFile(
		t,
		dir,
		locofile.File{
			Partial:  resourceName,
			Services: map[string]locofile.Service{resourceName: platform.ImageService(p.PublicImage)},
		},
	)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	resource := w.ResourceID(t, resourceName)
	name := "init-capacity-" + platform.RandomHex(4)
	p.MustApply(t, fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
 name: %s
 namespace: %s
 labels:
  app.kubernetes.io/managed-by: loco-controller
  loco.io/organization-id: %s
  loco.io/workspace-id: %s
  loco.io/environment-id: %s
  loco.io/resource-id: %s
spec:
 restartPolicy: Always
 securityContext:
  runAsNonRoot: true
  seccompProfile: {type: RuntimeDefault}
 initContainers:
 - name: init
   image: %s
   command: [sh, -c, 'exit 0']
   resources:
    requests: {cpu: 800m, memory: 256Mi}
   securityContext: &security
    allowPrivilegeEscalation: false
    capabilities: {drop: [ALL]}
 containers:
 - name: runtime
   image: %s
   resources:
    requests: {cpu: 200m, memory: 128Mi}
   securityContext: *security
`, name, w.Namespace, w.OrgID, w.ID, w.EnvironmentID, resource, p.PublicImage, p.PublicImage))
	t.Cleanup(func() {
		if _, err := p.Kubectl(
			context.Background(),
			"-n",
			w.Namespace,
			"delete",
			"pod",
			name,
			"--ignore-not-found",
		); err != nil {
			t.Error(err)
		}
	})
	p.MustKubectl(t, "-n", w.Namespace, "wait", "--for=condition=Ready", "pod/"+name, "--timeout=2m")
	var pod corev1.Pod
	p.MustGet(t, &pod, "-n", w.Namespace, "pod", name)
	var source, captured string
	platform.Eventually(
		t,
		3*time.Minute,
		"effective init-container requests reach ClickHouse",
		func(ctx context.Context) error {
			rows, err := p.ClickHouse(
				ctx,
				fmt.Sprintf(
					`SELECT DISTINCT Attributes['source_id'],toString(toUnixTimestamp64Milli(TimeUnix)),MetricName,Value,
Attributes['workspace_id'],Attributes['environment_id'],Attributes['resource_id'],Attributes['node']
FROM otel_metrics_gauge WHERE bitAnd(Flags,1)=0 AND WorkspaceId='%s' AND ResourceAttributes['loco.cluster.id']='%s'
AND Attributes['pod_uid']='%s' AND MetricName IN ('%s','%s')
ORDER BY TimeUnix DESC LIMIT 2`,
					systemtenant.WorkspaceID,
					p.ClusterID,
					pod.UID,
					capacitymetrics.CPU,
					capacitymetrics.Memory,
				),
			)
			if err != nil {
				return err
			}
			if len(rows) != 2 {
				return fmt.Errorf("received %d request dimensions", len(rows))
			}
			expected := map[string]float64{capacitymetrics.CPU: 0.8, capacitymetrics.Memory: 256 << 20}
			seen := map[string]bool{}
			for _, row := range rows {
				seen[row[2]] = true
				value, err := strconv.ParseFloat(row[3], 64)
				if err != nil {
					return err
				}
				scopeMatches := row[4] == w.ID && row[5] == w.EnvironmentID
				placementMatches := row[6] == resource && row[7] == pod.Spec.NodeName
				identityMatches := scopeMatches && placementMatches
				if value != expected[row[2]] || !identityMatches {
					return fmt.Errorf("capacity metric lost effective requests or identity: %v", row)
				}
			}
			if !seen[capacitymetrics.CPU] || !seen[capacitymetrics.Memory] {
				return fmt.Errorf("missing request dimension: %v", rows)
			}
			source, captured = rows[0][0], rows[0][1]
			return completeCapacitySample(ctx, source, captured)
		},
	)
	p.MustKubectl(t, "-n", w.Namespace, "delete", "pod", name, "--wait=true")
	platform.Eventually(
		t,
		3*time.Minute,
		"next complete collection excludes the deleted pod",
		func(ctx context.Context) error {
			rows, err := p.ClickHouse(
				ctx,
				fmt.Sprintf(`SELECT Attributes['source_id'],toString(toUnixTimestamp64Milli(TimeUnix))
FROM otel_metrics_gauge WHERE bitAnd(Flags,1)=0 AND WorkspaceId='%s' AND ResourceAttributes['loco.cluster.id']='%s'
AND MetricName='%s' AND TimeUnix>fromUnixTimestamp64Milli(%s) ORDER BY TimeUnix DESC LIMIT 1`,
					systemtenant.WorkspaceID, p.ClusterID, capacitymetrics.Pods, captured),
			)
			if err != nil {
				return err
			}
			if len(rows) != 1 {
				return errors.New("no newer capacity collection")
			}
			if err = completeCapacitySample(ctx, rows[0][0], rows[0][1]); err != nil {
				return err
			}
			count, err := p.ClickHouseCount(ctx, fmt.Sprintf(`SELECT count() FROM otel_metrics_gauge
WHERE bitAnd(Flags,1)=0 AND WorkspaceId='%s' AND ResourceAttributes['loco.cluster.id']='%s'
AND Attributes['source_id']='%s'
AND TimeUnix=fromUnixTimestamp64Milli(%s) AND Attributes['pod_uid']='%s'`,
				systemtenant.WorkspaceID, p.ClusterID, rows[0][0], rows[0][1], pod.UID))
			if err != nil {
				return err
			}
			if count != 0 {
				return errors.New("deleted pod remains in current capacity collection")
			}
			return nil
		},
	)
	t.Log(
		"800m/256Mi init request reached ClickHouse; next complete collection excludes the deleted pod",
	)
}

func completeCapacitySample(ctx context.Context, source, captured string) error {
	rows, err := p.ClickHouse(ctx, fmt.Sprintf(
		`SELECT uniqExactIf(Attributes['pod_uid'],MetricName='%s'),
uniqExactIf(Attributes['pod_uid'],MetricName='%s'),maxIf(Value,MetricName='%s')
FROM otel_metrics_gauge WHERE bitAnd(Flags,1)=0 AND WorkspaceId='%s' AND ResourceAttributes['loco.cluster.id']='%s'
AND Attributes['source_id']='%s' AND TimeUnix=fromUnixTimestamp64Milli(%s)
AND MetricName IN ('%s','%s','%s')`,
		capacitymetrics.CPU,
		capacitymetrics.Memory,
		capacitymetrics.Pods,
		systemtenant.WorkspaceID,
		p.ClusterID,
		source,
		captured,
		capacitymetrics.CPU,
		capacitymetrics.Memory,
		capacitymetrics.Pods,
	))
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return errors.New("capacity collection is missing")
	}
	if rows[0][0] != rows[0][2] || rows[0][1] != rows[0][2] {
		return fmt.Errorf("capacity collection is partial: %v", rows)
	}
	return nil
}
