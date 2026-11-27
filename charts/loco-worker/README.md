# loco-worker

`loco-worker` installs everything Loco runs on a worker cluster: the agent, the Application and build controllers, Envoy Gateway with the application Gateway, and the OpenTelemetry collectors. Install it into `loco-system` after cert-manager and `mise run crds:apply`. Create `loco-system` first with the Pod Security labels in `manifests/namespaces/loco-system.yaml`; the chart creates `loco-obs`, `loco-obs-node` and, when `cluster.role` is `builds` or `all`, `loco-builds`. Provide the agent, controller and builder image tags, the control plane URL, `global.region`, `global.exporter.endpoint` and, for the first start, a join token from `locoadm cluster create`.

The agent reads `agent.joinToken.existingSecret` (key `agent.joinToken.secretKey`) through an optional volume at `LOCO_JOIN_TOKEN_FILE`. It waits with backoff for the file and rereads it until enrollment succeeds, so creating or renewing the Secret requires no restart. Keep credentials in standalone Secrets. After enrollment the agent uses its persisted identity even if the token Secret is removed.

## Resources

The Go runtime sets `GOMAXPROCS` from the container CPU limit, rounding a limit below 2 up to 2. The agent, both controllers, the builder's fetch and push steps and the OpenTelemetry collectors have a CPU limit of at least 2; Envoy, Envoy Gateway and the metrics server have none. The agent, the controllers and the builder set no Go memory limit. Envoy Gateway gets `GOMEMLIMIT` equal to its memory limit, the collector chart sets 80% of each collector's, and the metrics server has none. Each build's BuildKit step requests 1 CPU and 1Gi and may use 4 CPUs and 4Gi.

## Application rollouts

`controller.minReadySeconds` requires a new replica to stay ready before it replaces an old one. `controller.drainSeconds` is the positive whole-second delay before Kubernetes signals a retiring service container to stop, defaulting to 5. The chart supplies it as `LOCO_DRAIN_SECONDS`; an Application controller run outside the chart must set that variable too. Kubernetes runs the delay as a native preStop sleep, so application images need no shell or sleep command.

The delay lets routing remove the retiring endpoint while the process accepts traffic. Increase it when endpoint withdrawal takes longer in the cluster; it does not guarantee convergence while the control plane or routing is stalled. The Pod's total termination grace is the drain delay plus Kubernetes' default 30-second process shutdown budget. Workers without ports stop without the delay. Changing the drain setting rolls serving Pod templates, and each Pod gets the hook when it is replaced.

`controller.healthyAfterSeconds`, 300 by default, is how long every replica of a rollout must run without a restart before the rollout counts as healthy. The chart supplies it as `LOCO_HEALTHY_AFTER_SECONDS`.

## Controller metrics and traces

Each controller serves the controller-runtime Prometheus metrics at `/metrics` on port 8080. They include reconcile time (`controller_runtime_reconcile_time_seconds`), work queue depth and wait (`workqueue_depth`, `workqueue_queue_duration_seconds`) and Kubernetes API client latency and rate-limiter wait (`rest_client_request_duration_seconds`, `rest_client_rate_limiter_duration_seconds`). The release namespace denies ingress, so read them with `kubectl port-forward deploy/loco-controller 8080 -n loco-system`. Adding `--metrics-bind-address=0` to `controller.args` or `builds.controller.args` turns the endpoint off.

Setting `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` in `controller.env` or `builds.controller.env` exports that controller's traces over OTLP/HTTP, as `loco-controller` or `loco-build-controller`; the SDK reads the other `OTEL_*` variables too. Each Application reconcile is an `application.reconcile` span with a child span per step and a client span per Kubernetes API call; each Build reconcile is a `build.reconcile` span. When the API request that created the deployment or build was traced, the agent records its trace context in the object's `loco.io/traceparent` annotation and each reconcile span links to that request's span. Without either variable the controllers export no traces.

```yaml
controller:
  env:
    - name: OTEL_EXPORTER_OTLP_ENDPOINT
      value: http://collector.example.com:4318
```

## Resource metrics

The chart installs metrics-server for application autoscaling; set `metrics-server.enabled: false` when the cluster already serves `metrics.k8s.io`.

If kubelet serving certificates are not signed by the cluster CA, configure CA-signed certificates or explicitly set `metrics-server.args: ["--kubelet-insecure-tls"]` (installation spec: [`workers[].metricsServer.kubeletInsecureTLS: true`](../../docs/content/reference/installation.md)) to disable kubelet certificate verification.
An unavailable metrics APIService can block API discovery and namespace deletion, so restore metrics-server readiness before tearing down workspaces.

## Replicas and disruption budgets

The Application and build controllers run `controller.replicas` and `builds.controller.replicas` replicas, 2 by default. The chart passes `--leader-elect` to both, whatever `controller.args` and `builds.controller.args` hold, so one replica reconciles while the others wait for its lease. Envoy Gateway and metrics-server run 2 replicas. The Gateway's Envoy proxies scale on CPU between `envoyProxy.autoscaling.minReplicas` and `maxReplicas`, 2 to 6 by default, or run `envoyProxy.replicas` with `envoyProxy.autoscaling.enabled: false`. The gateway collector, `otel-col-deploy`, runs a fixed `otel-col-deploy.replicaCount` of 2 with no autoscaler, because each replica queues exports on its own volume and a removed replica strands its queue; raise `otel-col-deploy.resources` to give it more capacity, and `global.exporter.queue.num_consumers` to drain a backlog behind a slow endpoint. The agent runs one replica, the schema's maximum: every agent replica uses the cluster's one identity. The two controllers, Envoy Gateway, metrics-server, the Gateway's Envoy proxies and `otel-col-deploy` each have a PodDisruptionBudget that lets one pod go at a time, whatever their replica count, and spread their pods across nodes by hostname with `whenUnsatisfiable: ScheduleAnyway`, so a one-node cluster still schedules them. The agent and the cluster collector, `otel-col-cluster`, have neither.

## Gateway certificates

Envoy Gateway uses `GatewayNamespace` deployment mode so each proxy runs beside its Gateway. Application ingress policies require that placement. When reusing a controller, keep `config.envoyGateway.provider.kubernetes.deploy.type: GatewayNamespace`; the chart's bundled controller defaults to this mode.

With an HTTPS `env.CONTROL_PLANE_URL`, the gateway serves a Loco error page for unmatched application hostnames through the control-plane API. Envoy validates the API's TLS certificate against system trust roots. The chart creates a `<release>-fallback` namespace for the route and its external backend. A separately installed Envoy Gateway controller requires `config.envoyGateway.extensionApis.enableBackend: true`. HTTP control-plane URLs do not enable the fallback.

The Gateway terminates TLS with a Secret in the Helm release's namespace. By default the chart creates a Let's Encrypt ACME ClusterIssuer and one wildcard Certificate per app domain in `global.domain.apps`: `<release>-<domain>` covers `*.<domain>` and writes the Secret `<release>-<domain>-tls`, which that domain's HTTPS listener serves. Each listener's certificate covers only its own domain, so Envoy Gateway negotiates HTTP/2 on every app listener. Each entry in `certManager.additionalDNSNames` joins the Certificate of the longest app domain it falls under, and rendering fails for an entry under none.

Upgrading from chart 0.4.7 or earlier replaces the shared Certificate `<release>-cert`. Each app listener serves no HTTPS until cert-manager issues its domain's Certificate, about as long as one ACME order takes. cert-manager leaves the old Secret `<release>-tls` in place; nothing reads it, and `kubectl -n <release namespace> delete secret <release>-tls` removes it. This mode needs `certManager.issuer.email` and `certManager.issuer.solvers`, cert-manager's own `solvers` list, and rendering fails without them. The chart renders the list verbatim and creates no credentials. Create any Secret a solver references in the `cert-manager` namespace yourself.

The Certificates cover wildcard names, which ACME issues only through DNS-01, so use a DNS-01 solver for your DNS provider, for example Cloudflare, Route53, Cloud DNS, Azure DNS, DigitalOcean or a webhook solver. This one reads a Cloudflare API token from the Secret `cloudflare-api-token`:

```yaml
certManager:
  issuer:
    email: ops@example.com
    solvers:
      - dns01:
          cloudflare:
            apiTokenSecretRef:
              name: cloudflare-api-token
              key: api-token
```

```sh
kubectl -n cert-manager create secret generic cloudflare-api-token --from-literal=api-token="$TOKEN"
```

For a local installation, issue a self-signed certificate without ACME:

```yaml
certManager:
  issuer:
    name: loco-local
    type: selfSigned
```

Self-signed certificates require clients to trust the certificate explicitly. They do not provide public browser trust.

To use an issuer managed outside this release, create the Certificates with that issuer's name and kind. An `Issuer` must exist in the release's namespace; a `ClusterIssuer` is cluster scoped.

```yaml
certManager:
  issuer:
    create: false
    name: organization-ca
    kind: ClusterIssuer
```

To use an existing TLS Secret, set its name. This skips the chart's Certificates and ClusterIssuer. The supplied Secret must exist in the release's namespace and cover every configured Gateway hostname. Every HTTPS listener serves it, so with more than one app domain the listeners' certificates overlap and Envoy Gateway serves them HTTP/1.1 only.

```yaml
certManager:
  existingTLSSecret: gateway-tls
```

`envoyProxy.http3: true` serves HTTP/3 on the app listeners through a ClientTrafficPolicy, and Envoy Gateway then adds UDP 443 to the Envoy Service next to TCP 443. It is off by default: a cloud load balancer that keys its configuration by port alone rejects TCP and UDP on the same port, and then never syncs node changes. `envoyProxy.tls.minVersion` renders the same ClientTrafficPolicy without HTTP/3. `envoyProxy.envoyHeaders: true` sets `headers.enableEnvoyHeaders` on that policy, so Envoy adds its `x-envoy-*` headers to responses, among them `x-envoy-upstream-service-time`, the milliseconds the upstream took. Every client then sees per-request upstream timing, which sharpens timing attacks. It is off by default; keep it off in production.

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

## Registry credentials

`registry.auth` selects how the worker cluster authenticates to the image registry, and must match the API's `LOCO_REGISTRY_AUTH`. The chart passes it to the agent and both controllers as `LOCO_REGISTRY_AUTH`.

With `static`, the default, the operator creates `kubernetes.io/dockerconfigjson` Secrets in the release namespace and names them in `registry.pullSecretName` and `builds.pushSecretName`. The Application controller copies the pull Secret into each environment namespace as `loco-registry-pull` and attaches it to every workload ServiceAccount. The build controller copies both Secrets into the builds namespace as `registry-pull` and `registry-push`, mounts the pull Secret in the build's `fetch` step and the push Secret in its `push` step, and fails every build when `builds.pushSecretName` is empty. The chart lets the build controller `get` only the Secrets these two values name in the release namespace.

```yaml
registry:
  pullSecretName: loco-registry
builds:
  pushSecretName: loco-registry-push
```

With `token`, the cluster holds no shared registry Secret and the chart fails when `registry.pullSecretName` or `builds.pushSecretName` is set. The agent writes `loco-registry-pull-<workspace id>` in the release namespace before it applies a workspace's first Application, and the Application controller copies it into each environment namespace as `loco-registry-pull`. Until the copy source exists, the Application stays `Deploying` with the message `Waiting for the agent to write the registry pull secret...`. For each build the agent writes `build-<build id>-registry` in the builds namespace, and the build controller mounts it in both the `fetch` and `push` steps.

```yaml
registry:
  auth: token
```

## OpenTelemetry collectors

The OpenTelemetry collectors store nothing: every pipeline exports over OTLP/HTTP to `global.exporter.endpoint`.

| Collector | Collects |
| --- | --- |
| `otel-col-deploy` (StatefulSet) | OTLP from applications and Envoy |
| `otel-col-daemon` (DaemonSet) | Container logs, except from `kube-system`, `flux-system` and `cert-manager`, and kubelet metrics |
| `otel-col-cluster` (StatefulSet) | Kubernetes cluster metrics and events |

```yaml
global:
  region: us-ca-1
  exporter:
    endpoint: https://ingest.region.example.com
    headers: {}
    ingestToken:
      enabled: true
      secretName: loco-otlp-exporter
```

With `ingestToken.enabled`, each collector sends the Secret's `token` key as a bearer token. The Secret lives in the `loco-obs` and `loco-obs-node` namespaces. The agent writes the cluster's ingest token to `secretName` and `secretKey` in both; the chart lets the agent create Secrets in those namespaces and `get` and `update` only that one. Each collector pod waits in its `exporter-token-wait` init container until the token file is not empty.

Each collector persists requests before batching them for export (`global.exporter.queue`) and retries until the endpoint accepts them (`global.exporter.retry`). The DaemonSet keeps its queue in a hostPath directory per namespace and collector, `/var/lib/otelcol-exporter/<namespace>/<name>`, which its init container hands to the collector user. The two StatefulSets keep theirs on a 2Gi PersistentVolumeClaim per replica, owned through `fsGroup`. Batching uses `global.exporter.queue.batch`: 100 items or a 1s flush timeout by default. A full queue blocks senders (`block_on_overflow: true`). Queues survive control-plane outages and pod replacement; a claim outlives a scale-down and replays when its replica returns.

### Kubernetes platform telemetry

`global.platformTelemetry.enabled` adds platform pipelines to the node and cluster collectors: container logs, node metrics, cluster state and Kubernetes events from the release namespace, the collector namespaces and `global.platformTelemetry.namespaces`. Every platform resource carries `loco.installation.id`, `k8s.cluster.name`, `deployment.environment.name`, `loco.region` and `loco.telemetry.scope=platform`. The collectors delete `loco.telemetry.scope` from the telemetry workloads send, so only these pipelines set it.

```yaml
global:
  platformTelemetry:
    enabled: true
    installation: local
    cluster: kind-local
    environment: development
```

The controllers export their traces and metrics, including the controller-runtime metrics their Prometheus endpoint serves, to the cluster collector's platform pipelines at `otel-col-cluster-platform` on port 4318. A NetworkPolicy admits only the `loco-controller` and `loco-build-controller` pods of the release namespace to that port. Each controller pod sets `service.instance.id` to its pod UID and `k8s.namespace.name` to the release namespace through `OTEL_RESOURCE_ATTRIBUTES`, so two replicas export separate series. Application rollout changes include availability, health and reported failure details, with trace and span IDs in their JSON logs. Routine reconcile messages remain at DEBUG; set `controller.args: ["--log-level=debug"]` or `builds.controller.args: ["--log-level=debug"]` to include them. Both controllers use JSON logs at INFO by default. `controller.env` and `builds.controller.env` cannot set an OTLP endpoint or headers variable, such as `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`, or `OTEL_RESOURCE_ATTRIBUTES`, while platform telemetry is on.

The existing DaemonSet runs one collector per node and tails the container log files. Its platform pipeline collects API, controller, agent and observability-proxy stdout from allowed platform namespaces, enriches Kubernetes and service identity, and restores the JSON log's timestamp, severity, trace/span IDs and typed attributes. Workload namespaces go through the workload log pipeline and retain their original bodies. The pipelines use disjoint namespace filters, so a file log is exported once. All collector pods' own logs and the proxy's marked ingestion failure diagnostics stay excluded from forwarding to prevent collection loops; those diagnostics remain available through pod logs. File checkpoints and the persistent exporter queue survive collector pod replacement; node loss or log rotation before collection can still lose records. API and controller processes have no OTLP log exporter.

Platform pipelines export through `global.exporter` by default, so the observability proxy stores them under the system tenant. `global.platformTelemetry.exporter.endpoint` sends them only to that OTLP/HTTP endpoint instead, with `exporter.headers` and without the ingest token, while application telemetry keeps using `global.exporter`:

```yaml
global:
  platformTelemetry:
    exporter:
      endpoint: https://otel.example.com
      headers:
        Authorization: Bearer EXAMPLE_TOKEN
```

`platformOnly: true` with `otel-col-deploy.enabled: false` keeps only the platform pipelines. Names, units and availability follow the pinned [kubelet statistics](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/v0.165.0/receiver/kubeletstatsreceiver) and [cluster](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/v0.165.0/receiver/k8sclusterreceiver) receivers.

```sh
mise run test:collectors
mise run e2e controlplane TestClusterMetrics
```
