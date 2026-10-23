# Kubernetes platform telemetry

`loco-obs` can export node metrics, cluster state and platform Kubernetes events through a separate OpenTelemetry pipeline. Enable it with `global.platformTelemetry.enabled`. ClickHouse is the default destination; `otlp_http` sends to a configurable OTLP endpoint.

```yaml
global:
  region: local
  platformTelemetry:
    enabled: true
    installation: local
    cluster: kind-local
    environment: development
    collectionInterval: 60s
    namespaces:
      - loco-system
      - observability
      - observability-node
```

Render the configuration with the chart dependencies installed:

```sh
mise run helm:deps
helm template loco-obs ./charts/loco-obs --namespace observability --values platform-values.yaml
```

The node DaemonSet collects host and kubelet metrics. The single cluster collector supplies Kubernetes state and events. The ingest collector's autoscaling does not create additional cluster watchers. Platform node collection has its own interval; the cluster receiver shares its interval with the existing cluster telemetry pipeline.

Platform metrics include CPU, memory, storage and network measurements, node conditions and capacity, workload readiness and container restarts. Names, units and availability follow the pinned [host metrics](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/v0.162.0/receiver/hostmetricsreceiver), [kubelet statistics](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/v0.162.0/receiver/kubeletstatsreceiver) and [cluster](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/v0.162.0/receiver/k8sclusterreceiver) receivers. Filesystem and volume measurements depend on what the node exposes. An absent measurement is unavailable; it is not zero or healthy.

The namespace list selects platform workload metrics and events. Cluster-wide and node measurements have no namespace and remain included. This selection applies only to the new pipeline. Existing application telemetry and its destination remain unchanged. This does not replace the collection and retention policy tracked in #855.

Every platform resource carries `loco.installation.id`, `k8s.cluster.name`, `deployment.environment.name`, `loco.region` and `loco.telemetry.scope=platform`. Query that scope when reading platform records. The original pipelines remain active by default, so ClickHouse may also contain their original records without this scope.

## Send to an OTLP destination

```yaml
global:
  platformTelemetry:
    destination: otlp_http
    otlp:
      endpoint: https://metrics.example.test
      headers:
        Authorization: ${env:PLATFORM_AUTHORIZATION}
      tls:
        insecure: false
        insecure_skip_verify: false

otel-col-daemon:
  extraEnvsFrom:
    - secretRef:
        name: platform-export

otel-col-cluster:
  extraEnvsFrom:
    - secretRef:
        name: platform-export
```

Merge this with the identity settings above and replace the endpoint with the chosen destination. Create `platform-export` in both collector namespaces with a `PLATFORM_AUTHORIZATION` key containing the required authorization header. Kubernetes supplies the Secret through the environment; its value does not enter the ConfigMap. Missing referenced Secrets prevent the collector pod from starting. Restart collectors after rotating environment-backed credentials.

Use the collector chart's `extraVolumes` and `extraVolumeMounts` for a private CA, then set `otlp.tls.ca_file` to its mounted path. Keep certificate verification enabled. OTLP export uses outbound connections; the backend needs no access to private Kubernetes endpoints.

`platformTelemetry.clickhouse` accepts ClickHouse exporter overrides, including endpoint, database, username and a password supplied through an environment variable. Omitted fields inherit the existing ClickHouse exporter. The default uses the existing schema and migrations. For a separate database, provision a compatible schema or explicitly select the exporter's `create_schema` behavior and permissions.

## Collect platform data without application telemetry

For an installation that needs only platform telemetry, merge these values with the OTLP and identity configuration:

```yaml
global:
  platformTelemetry:
    platformOnly: true

clickhouse:
  enabled: false

otel-col-deploy:
  enabled: false

otel-col-daemon:
  extraEnvs: []

otel-col-cluster:
  extraEnvs: []
```

`platformOnly` removes the original pipelines from the node and cluster collectors. `extraEnvs: []` removes their default ClickHouse password reference; keep the OTLP Secret references from the previous example. Application telemetry ingestion and queries require their own collectors, storage and observability proxy if used. This configuration does not provide them.

## Export failures and local verification

`platformTelemetry.sendingQueue` and `platformTelemetry.retry` configure bounded in-memory export buffering. The defaults retain at most 256 batches and retry a batch for up to 60 seconds. Longer failures or process restarts can lose data. Replayed records keep their original observation timestamps.

The collectors expose their normal internal telemetry. Monitor exporter failures, queue occupancy, refused or dropped records and receipt of recent samples at the destination. A running collector is not proof that export works. Platform export to OTLP does not depend on local ClickHouse, although collectors still share process resources with any enabled application pipelines.

```sh
mise run test:chart-contracts
mise run e2e:cluster-metrics
```

The end-to-end suite creates its own kind cluster, checks node and cluster measurements and namespace-scoped events in authenticated OTLP and ClickHouse destinations, and stops the OTLP destination to check retry and timestamp behavior. It deletes its resources afterward. Use `--no-teardown` to keep that test cluster for a rerun.
