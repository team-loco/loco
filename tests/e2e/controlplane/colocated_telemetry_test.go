package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/team-loco/loco/internal/systemtenant"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func (cp *controlPlane) checkCoLocatedCollection(t *testing.T, reader, password, clickhousePod string) {
	t.Helper()
	var original appsv1.StatefulSet
	c.MustGet(t, &original, "-n", cp.values.ClusterCollector.NamespaceOverride, "statefulset", "cluster-collector")
	queueClaims := make([]corev1.PersistentVolumeClaim, 0, len(original.Spec.VolumeClaimTemplates))
	for _, template := range original.Spec.VolumeClaimTemplates {
		var claim corev1.PersistentVolumeClaim
		c.MustGet(t, &claim, "-n", original.Namespace, "pvc", template.Name+"-"+original.Name+"-0")
		queueClaims = append(queueClaims, claim)
	}
	if len(queueClaims) == 0 {
		t.Fatal("cluster collector has no persistent queue claims")
	}
	cp.upgrade(t, "nodeCollector.enabled=false", "clusterCollector.enabled=false")
	defer cp.upgrade(t)
	cpNodeNamespace := cp.values.NodeCollector.NamespaceOverride
	cpClusterNamespace := cp.values.ClusterCollector.NamespaceOverride
	platform.Eventually(
		t,
		metricsTimeout,
		"control-plane collectors finish terminating before delegation",
		func(ctx context.Context) error {
			var pods corev1.PodList
			if err := c.Get(ctx, &pods, "pods", "-A", "-l", "app.kubernetes.io/instance="+release); err != nil {
				return err
			}
			for _, pod := range pods.Items {
				if pod.Namespace == cpNodeNamespace || pod.Namespace == cpClusterNamespace {
					return fmt.Errorf("control-plane collector %s/%s is still running", pod.Namespace, pod.Name)
				}
			}
			return nil
		},
	)
	for _, originalClaim := range queueClaims {
		var retained corev1.PersistentVolumeClaim
		c.MustGet(t, &retained, "-n", originalClaim.Namespace, "pvc", originalClaim.Name)
		if retained.UID != originalClaim.UID || retained.DeletionTimestamp != nil {
			t.Fatalf("delegation did not retain queue claim %s/%s", retained.Namespace, retained.Name)
		}
	}
	for _, workload := range []string{release + "-api", release + "-obs-proxy-ingest"} {
		rollout(t, cp.namespace, "deployment/"+workload)
		var deployment appsv1.Deployment
		c.MustGet(t, &deployment, "-n", cp.namespace, "deployment", workload)
		if deployment.Spec.Template.Labels["loco.io/platform-log-owner"] != "" {
			t.Fatal("a delegated control plane still claims stdout collection")
		}
	}

	const (
		nodeNamespace    = "colocated-worker-node"
		clusterNamespace = "colocated-worker-cluster"
		nodeName         = "colocated-node-collector-agent"
		clusterName      = "colocated-cluster-collector"
	)
	c.MustApply(t, namespace(nodeNamespace, "privileged")+"---\n"+namespace(clusterNamespace, "restricted"))
	var token corev1.Secret
	c.MustGet(t, &token, "-n", cp.namespace, "secret", release+"-system-ingest-token")
	for _, namespace := range []string{nodeNamespace, clusterNamespace} {
		applySecret(
			t,
			namespace,
			release+"-system-ingest-token",
			map[string]string{"token": string(token.Data["token"])},
		)
	}
	collectors := collectorManifest(
		t,
		clusterNamespace,
		"global.region="+cp.values.PlatformTelemetry.Region,
		"global.platformTelemetry.cluster=control-plane",
		"global.platformTelemetry.namespaces={"+cp.namespace+"}",
		fmt.Sprintf(
			"global.exporter.endpoint=http://%s-obs-proxy-ingest.%s:%d",
			release,
			cp.namespace,
			cp.values.ObsProxy.Ingest.HTTPPort,
		),
		"global.exporter.ingestToken.secretName="+release+"-system-ingest-token",
		"nodeCollector.namespaceOverride="+nodeNamespace,
		"nodeCollector.fullnameOverride=colocated-node-collector",
		"nodeCollector.config.extensions.file_storage.directory=/var/lib/otelcol/colocated-e2e",
		"clusterCollector.namespaceOverride="+clusterNamespace,
		"clusterCollector.fullnameOverride="+clusterName,
	)
	// This fixture uses the internal listener rather than public DNS. Its explicit
	// peer policy grants only these worker collectors access to that test endpoint.
	c.MustApply(t, fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: colocated-worker-ingest
  namespace: %[1]s
spec:
  podSelector:
    matchLabels: {app: %[2]s-obs-proxy-ingest}
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchExpressions:
              - {key: kubernetes.io/metadata.name, operator: In, values: [%[3]s, %[4]s]}
          podSelector:
            matchLabels: {app.kubernetes.io/instance: metrics}
      ports: [{protocol: TCP, port: %[5]d}]
`, cp.namespace, release, nodeNamespace, clusterNamespace, cp.values.ObsProxy.Ingest.HTTPPort))
	defer func() {
		if t.Failed() {
			c.LogKubectl(t,
				"-n "+nodeNamespace+" describe pods",
				"-n "+nodeNamespace+" logs daemonset/"+nodeName+" --all-containers=true --tail=80",
				"-n "+clusterNamespace+" describe pods",
				"-n "+clusterNamespace+" logs statefulset/"+clusterName+" --all-containers=true --tail=80",
			)
		}
		if _, err := c.KubectlInput(
			t.Context(),
			collectors,
			"delete",
			"--ignore-not-found",
			"--wait=true",
			"-f",
			"-",
		); err != nil {
			t.Error(err)
		}
	}()
	c.MustApply(t, collectors)
	rollout(t, nodeNamespace, "daemonset/"+nodeName)
	rollout(t, clusterNamespace, "statefulset/"+clusterName)
	for _, target := range []struct{ namespace, kind, name string }{
		{nodeNamespace, "daemonsets", nodeName},
		{clusterNamespace, "statefulsets", clusterName},
	} {
		owned := strings.Fields(c.MustKubectl(t, "-n", target.namespace, "get", target.kind,
			"-l", "app.kubernetes.io/instance=metrics", "-o", "jsonpath={.items[*].metadata.name}"))
		if len(owned) != 1 || owned[0] != target.name {
			t.Fatalf("delegated %s owners = %v, want only %s", target.kind, owned, target.name)
		}
	}
	cp.checkStartupLogDelivery(t, reader, password, clickhousePod)
	cp.checkInfrastructureCollection(t, reader, password, clickhousePod, "co-located worker owns platform collection")
	cutoff := time.Now().UnixNano()
	cp.request(t, http.MethodPost, cp.values.ControlPlaneHostname, "/loco.config.v1.ConfigService/GetConfig", "{}", nil).
		expect(t, http.StatusOK)
	var receipt string
	platform.Eventually(
		t,
		metricsTimeout,
		"direct control-plane SDK export survives collector delegation",
		func(ctx context.Context) error {
			row, err := c.Kubectl(
				ctx,
				"-n",
				cp.namespace,
				"exec",
				clickhousePod,
				"-c",
				"clickhouse",
				"--",
				"clickhouse-client",
				"--user",
				reader,
				"--password",
				password,
				"-q",
				fmt.Sprintf(`SELECT ServiceName, TraceId,
ResourceAttributes['loco.cluster.id'] AS cluster FROM loco_obs.otel_traces
WHERE WorkspaceId = '%s' AND ServiceName = 'loco-api'
AND Timestamp >= fromUnixTimestamp64Nano(%d) LIMIT 1 FORMAT JSONEachRow`, systemtenant.WorkspaceID, cutoff),
			)
			if err != nil {
				return err
			}
			receipt = strings.TrimSpace(row)
			if !json.Valid([]byte(receipt)) {
				return errors.New("no fresh API SDK trace after collector delegation")
			}
			return nil
		},
	)
	t.Logf("synthetic co-located installation: fresh direct API SDK trace: %s", receipt)
}
