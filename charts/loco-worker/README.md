# loco-worker

`loco-worker` installs everything Loco runs on a worker cluster: the agent, the Application and build controllers, Envoy Gateway with the application Gateway, and the OpenTelemetry collectors. Install it into `loco-system` after cert-manager and `mise run crds:apply`. Create `loco-system` first with the Pod Security labels in `manifests/namespaces/loco-system.yaml`; the chart creates `loco-obs`, `loco-obs-node` and, with `builds.enabled`, `loco-builds`. Provide the agent, controller and builder image tags, the control plane URL, `global.region`, `global.exporter.endpoint` and, for the first start, a join token from `locoadm cluster create`.

## Gateway certificates

The Gateway terminates TLS with a Secret in the Helm release's namespace. By default the chart creates a Let's Encrypt ACME ClusterIssuer, a wildcard Certificate, and a Cloudflare API-token Secret. Set `certManager.issuer.email` and `secrets.cloudflareToken` for this mode. The Cloudflare Secret renders only when the token is set.

To solve challenges another way, set `certManager.issuer.solvers` to cert-manager's own `solvers` list, for example Route53, Cloud DNS, Azure DNS, DigitalOcean, a webhook solver, or HTTP-01 through `gatewayHTTPRoute`. The list replaces the Cloudflare default and renders verbatim.

For a local installation, issue a self-signed certificate without ACME or Cloudflare:

```yaml
certManager:
  issuer:
    name: loco-local
    type: selfSigned
```

Self-signed certificates require clients to trust the certificate explicitly. They do not provide public browser trust.

To use an issuer managed outside this release, create the Certificate with that issuer's name and kind. An `Issuer` must exist in the release's namespace; a `ClusterIssuer` is cluster scoped.

```yaml
certManager:
  issuer:
    create: false
    name: organization-ca
    kind: ClusterIssuer
```

To use an existing TLS Secret, set its name. This skips the chart's Certificate, ClusterIssuer and Cloudflare Secret. The supplied Secret must exist in the release's namespace and cover every configured Gateway hostname.

```yaml
certManager:
  existingTLSSecret: gateway-tls
```


The Gateway's Envoy Service uses Envoy Gateway's defaults unless `envoyProxy.service` supplies native [EnvoyService settings](https://gateway.envoyproxy.io/docs/api/extension_types/#kubernetesservicespec). For a kind installation with host port mappings, expose HTTP on a fixed NodePort:

```yaml
envoyProxy:
  service:
    type: NodePort
    externalTrafficPolicy: Cluster
    patch:
      type: StrategicMerge
      value:
        spec:
          ports:
            - port: 80
              nodePort: 30080
```

Envoy Gateway creates and reconciles this Service. `externalTrafficPolicy: Cluster` allows a kind host mapping to reach the proxy even when the proxy pod runs on another node. Map the kind node's port `30080` to the desired host port. The patch merges the HTTP listener's generated Service port and preserves its generated target port.

## OpenTelemetry collectors

The OpenTelemetry collectors store nothing: every pipeline exports over OTLP/HTTP to `global.exporter.endpoint`.

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
    ingestToken:
      enabled: true
      secretName: loco-otlp-exporter
```

With `ingestToken.enabled`, each collector sends the Secret's `token` key as a bearer token. The Secret lives in the `loco-obs` and `loco-obs-node` namespaces. The agent writes the cluster's ingest token to `secretName` and `secretKey` in both; the chart lets the agent create Secrets in those namespaces and `get` and `update` only that one. Each collector pod waits in its `exporter-token-wait` init container until the token file is not empty.

Each collector queues unsent batches on disk (`global.exporter.queue`) and retries until the endpoint accepts them (`global.exporter.retry`). The DaemonSet keeps its queue in a hostPath directory per namespace and collector, `/var/lib/otelcol-exporter/<namespace>/<name>`, which its init container hands to the collector user. The two StatefulSets keep theirs on a 2Gi PersistentVolumeClaim per replica, owned through `fsGroup`. Queues survive control-plane outages and pod replacement; a claim outlives a scale-down and replays when its replica returns.

### Kubernetes platform telemetry

`global.platformTelemetry.enabled` adds platform pipelines to the node and cluster collectors: node metrics, cluster state and Kubernetes events from the release namespace, the collector namespaces and `global.platformTelemetry.namespaces`. They use the same exporter. Every platform resource carries `loco.installation.id`, `k8s.cluster.name`, `deployment.environment.name`, `loco.region` and `loco.telemetry.scope=platform`.

```yaml
global:
  platformTelemetry:
    enabled: true
    installation: local
    cluster: kind-local
    environment: development
```

`platformOnly: true` with `otel-col-deploy.enabled: false` keeps only the platform pipelines. Names, units and availability follow the pinned [kubelet statistics](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/v0.163.0/receiver/kubeletstatsreceiver) and [cluster](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/v0.163.0/receiver/k8sclusterreceiver) receivers.

```sh
mise run test:collectors
mise run e2e:cluster-metrics
```
