# loco-obs

`loco-obs` runs the OpenTelemetry collectors on a worker cluster. It stores nothing: every pipeline exports over OTLP/HTTP to `global.exporter.endpoint`.

| Collector | Collects |
| --- | --- |
| `otel-col-deploy` (StatefulSet) | OTLP from applications and Envoy |
| `otel-col-daemon` (DaemonSet) | Container logs, kubelet and host metrics, pulled application metrics |
| `otel-col-cluster` (StatefulSet) | Kubernetes cluster metrics and events |

```yaml
global:
  region: us-east-1
  exporter:
    endpoint: https://ingest.region.example.com
    headers: {}
    bearerToken:
      enabled: true
      secretName: loco-otlp-exporter
```

With `bearerToken.enabled`, each collector sends the Secret's `token` key as a bearer token. The Secret lives in the `observability` and `observability-node` namespaces.

Each collector queues unsent batches on disk (`global.exporter.queue`) and retries until the endpoint accepts them (`global.exporter.retry`). The DaemonSet keeps its queue in a hostPath directory per namespace and collector, `/var/lib/otelcol-exporter/<namespace>/<name>`, which its init container hands to the collector user. The two StatefulSets keep theirs on a 2Gi PersistentVolumeClaim per replica, owned through `fsGroup`. Queues survive control-plane outages and pod replacement; a claim outlives a scale-down and replays when its replica returns.

## Kubernetes platform telemetry

`global.platformTelemetry.enabled` adds platform pipelines to the node and cluster collectors: node metrics, cluster state and Kubernetes events from `global.platformTelemetry.namespaces`. They use the same exporter. Every platform resource carries `loco.installation.id`, `k8s.cluster.name`, `deployment.environment.name`, `loco.region` and `loco.telemetry.scope=platform`.

```yaml
global:
  platformTelemetry:
    enabled: true
    installation: local
    cluster: kind-local
    environment: development
```

`platformOnly: true` with `otel-col-deploy.enabled: false` keeps only the platform pipelines. Names, units and availability follow the pinned [host metrics](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/v0.163.0/receiver/hostmetricsreceiver), [kubelet statistics](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/v0.163.0/receiver/kubeletstatsreceiver) and [cluster](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/v0.163.0/receiver/k8sclusterreceiver) receivers.

```sh
mise run test:chart-contracts
mise run e2e:cluster-metrics
```
