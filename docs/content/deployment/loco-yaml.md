# loco.yaml

`loco.yaml` declares the services of a workspace: what to run, where, and how much. The CLI sends the file to the API, which plans the difference against an environment and applies it. The file is optional for a single service: `loco deploy` can deploy from the current directory with API defaults and a public hostname formed from the safe directory name and a four-letter suffix.

## Create a file

```sh
loco infra init --name storefront
```

`init` writes a starter `loco.yaml` from the API's defaults: the port, CPU, memory and replica counts, the default region, and a hostname under a platform domain the environment's cluster in that region serves. When that cluster serves none, `init` fails. The first line of the file points editors at the JSON Schema URL the API reports, `https://loco.build/schemas/loco.v1.json` on Loco's hosted service, which gives completion and inline errors. The dashboard serves the schema at `/schemas/loco.v1.json`.

```yaml
version: 1
partial: storefront

services:
  web:
    dockerfile: Dockerfile
    port: 8080
    health: { path: /healthz }
    domains: [storefront.example.com]
    env: { LOG_LEVEL: info }
    regions:
      us-east-1: { cpu: 250m, memory: 512Mi, replicas: { min: 1, max: 3 } }
  worker:
    image: ghcr.io/acme/worker:1.4.2
    regions:
      us-east-1: { cpu: 100m, memory: 256Mi, replicas: { min: 1, max: 1 } }
```

A service builds from source or runs a public `image`, never both. A source service builds in its `context`, a directory relative to `loco.yaml` that defaults to `.`, from its `dockerfile`, a path relative to `context` that defaults to `Dockerfile`. This service builds from `services/web/deploy/Dockerfile` with `services/web` as the build context:

```yaml
services:
  web:
    context: services/web
    dockerfile: deploy/Dockerfile
```

A service without `domains` is private. Each entry of `domains` is one label under a platform domain, such as `storefront.example.com` under `example.com`. A platform domain is one a cluster serves: the cluster's agent reports it once the Gateway listeners for `*.<domain>` are accepted and programmed and the certificate covering it is ready. The plan rejects a hostname unless the cluster of every region the service runs in serves its platform domain, and names the clusters that do not. When a cluster stops serving a domain, assigned hostnames stay, and the region's status turns degraded with the hostnames it no longer serves. Each region needs `cpu`, `memory`, `replicas.min` and `replicas.max`. Without `autoscaling`, the service runs `replicas.min` replicas. Set `autoscaling: { cpuTarget: 70 }` or `autoscaling: { memoryTarget: 80 }` on a region to scale between `replicas.min` and `replicas.max`. The target is a percentage of the container’s resource request, from 1 to 100. `loco.yaml` accepts exactly one target per region. Removing `autoscaling` returns the service to `replicas.min` replicas. Partial names are DNS labels. A service name is any label of at most 63 characters with a letter or digit; see [private service discovery](#private-service-discovery) for its slug. The API checks resource limits when it plans the file, and `GetConfig` reports them as `size_limits`.

## Pull application metrics

Declare a private HTTP endpoint to have Loco's OpenTelemetry Collector pull metrics from each running pod:

```yaml
services:
  web:
    metrics: { port: 9090, path: /metrics }
```

The endpoint serves Prometheus or OpenMetrics exposition. The collector converts samples into OpenTelemetry metrics and exports them to the control plane, which stores them in ClickHouse. Loco runs no Prometheus server. Query these metrics through the regional API, like metrics pushed over OTLP. Applications can continue to push OTLP without declaring this field; avoid exporting the same metric through both paths.

`port` is required and must be between 1024 and 65535. `path` defaults to `/metrics`. It must be an absolute path without a query or fragment. The collector uses HTTP to reach the pod directly, so a metrics endpoint needs no public domain or Kubernetes Service port. Each node's collector discovers only its own pods. Workspace and resource attribution comes from Kubernetes metadata.

Environment overrides can change the endpoint or set `metrics: null` to stop pulling. Applying a change updates the pod discovery metadata and the ingress rule for the collector. The chart's `otel-col-daemon.config.receivers.prometheus/applications.config.scrape_configs` configures the interval (30 seconds), timeout (10 seconds), sample limit (10,000 per scrape), and response body limit (10 MB).

The controller restricts metrics ingress to the node collector, which it finds through the `loco-worker` value `otel-col-daemon.namespaceOverride`.

## Private service discovery

Applications in the same workspace on the same cluster reach HTTP services through native Kubernetes DNS, such as `http://cart.loco-ws-00000000-0000-7000-8000-000000000003.svc.cluster.local`. Port 80 forwards to the configured application port, so callers omit the port even when cart listens on 7070. Loco uses namespaces named `loco-ws-<workspace UUID>`; replace the example UUID with the caller's workspace UUID. A caller in that namespace can also use `http://cart`. Both private and publicly routed services have these endpoints. Callers need no resource IDs, OIDC tokens or discovery API calls. Declare dependent services in regions where they run on the same cluster; these DNS names do not connect separate clusters or regions.

For example, a caller can pass dependency addresses through its environment:

```yaml
services:
  checkout:
    env:
      CART_URL: http://cart.loco-ws-00000000-0000-7000-8000-000000000003.svc.cluster.local
      PAYMENT_ADDRESS: payment.loco-ws-00000000-0000-7000-8000-000000000003.svc.cluster.local:50051
```

Use the cluster's configured DNS suffix in place of `cluster.local` when it has a custom cluster domain. Namespace-qualified names distinguish identical service names in different workspaces. Loco uses the cluster's DNS. Namespace-qualified names identify Services; network policies control which callers can reach them.

The operator creates a ClusterIP Service named after the slug. It exposes TCP port 80 and the configured application port, both forwarding to that application port. When the application uses port 80, the Service exposes it once. gRPC and other TCP clients can use the configured port or port 80 explicitly; their default port depends on the client protocol. A non-HTTP service can omit `health` to avoid HTTP probes. Short names such as `cart:7070`, the existing `resource-<UUID>:80` endpoint and public HTTP routes continue to work.

A resource's name is a display label of at most 63 characters. Its private hostname and the subdomain `loco deploy` generates use the name's slug: the name lowercased, with each run of characters other than letters and digits replaced by one hyphen, such as `cart` for `Cart` and `shopping-cart` for `Shopping Cart`. Slugs are unique within a workspace: creating or renaming a resource to a name whose slug another resource holds fails and names that resource. A name without a letter or digit has no slug and is rejected. Digit-leading slugs require [`RelaxedServiceNameValidation`](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/), enabled by default in Kubernetes 1.36 and stable in 1.37. The slugs `localhost` and `resource-<UUID>` are reserved, and new resources and renames reject names that produce them.

Renaming a resource through the Resource API moves its logical endpoint without an image build. The operator creates the new alias before removing the old alias. Port changes update the logical endpoint while preserving its ClusterIP. Resource deletion removes its aliases. Replacing a resource with another of the same slug waits for the previous resource to release it. An unmanaged Service or a Service owned by another Application causes reconciliation to fail; the operator does not adopt or overwrite it.

Workspace namespaces separate identical names in different workspaces. The workspace network policies permit communication between all applications in that namespace. Other workspaces remain isolated, and each environment runs on its own clusters.

Upgrade the API, agent, Application controller and operator CRDs together. Applications carrying a logical name gain the port 80 endpoint on controller reconciliation.

## Runtime user

Set `runAsUser` and `runAsGroup` to positive numeric IDs when an image uses a named user that Kubernetes cannot verify as non-root:

```yaml
services:
  accounting:
    runAsUser: 1654
    runAsGroup: 1654
```

These fields override the image's UID and primary GID independently. Omitted fields use the image defaults; an environment override can replace an ID or remove it with `null`. IDs must be between 1 and 2147483647. Loco continues to enforce non-root execution, disabled privilege escalation, dropped capabilities and the runtime's default seccomp profile.

Choose IDs that can read the application and write its working directories. The override does not create users, change file ownership or make an application that requires root compatible. Changes roll out new pods without rebuilding the image. Upgrade the API, controller and Application CRD together before using these fields.

## Health checks

Omit `health` for a TCP readiness check: a replica is ready once it accepts connections on `port`, Loco creates no liveness probe, and Kubernetes restarts exited containers. This suits gRPC and other TCP services without an HTTP health endpoint. A service that never listens on `port`, such as a queue worker, never becomes ready, and its deployment fails at the rollout's progress deadline; use protocol-specific E2E checks to verify that it can serve requests.

Set `health: {}` to enable HTTP liveness and readiness checks with the API defaults, or configure `path`, `interval`, `timeout`, `failThreshold` and `startupGracePeriod`. An environment override with `health: null` removes an inherited check. Removing `health` from an existing service redeploys it with the TCP readiness check.

A rollout starts new replicas next to the old ones, up to a quarter more than the desired count, and stops an old replica only after a new one has stayed ready for the worker cluster's `controller.minReadySeconds` (10 seconds by default). A new version that crashes, never accepts connections or fails its health check therefore receives no traffic and replaces no old replica: the deployment fails and the previous version keeps serving with all its replicas.

Check the structure without calling the API:

```sh
loco infra validate
```

## Failed applies

When a cluster's agent cannot apply a deployment and will not retry it itself, for example because Kubernetes rejects the Application, the API sends the deployment again after a delay. Once the cluster has failed `attempts` times, the API marks the deployment failed and stops sending it until the next deployment of the service. `deploy` sets both for one service:

```yaml
services:
  web:
    deploy: { attempts: 3, retryDelay: 60 }
```

`attempts` counts every try, including the first, and must be between 1 and 10. `retryDelay` is the number of seconds between tries and must be at least 60. An omitted field uses the API default: 2 attempts, 300 seconds apart, unless the operator sets `LOCO_DEFAULT_DEPLOY_ATTEMPTS` or `LOCO_DEFAULT_DEPLOY_RETRY_DELAY` within the same bounds. `GetConfig` reports the defaults as `deploy_attempts` and `deploy_retry_delay_seconds`. `deploy` applies to the service in every environment, so an environment override cannot set it. A change takes effect at the next failure. When `deploy` is the only change, `loco infra apply` updates the service without redeploying it, even while a deployment is rolling out; `loco deploy` still builds and redeploys the services it targets.

## Environment overrides

`environments.<name>` changes a service for one environment. Objects merge key by key, lists and scalars replace the whole value, and `null` removes a key. `enabled: false` leaves the service out of that environment.

```yaml
services:
  web:
    dockerfile: Dockerfile
    port: 8080
    domains: [staging.storefront.example.com]
    regions:
      us-east-1: { cpu: 250m, memory: 512Mi, replicas: { min: 1, max: 1 } }
    environments:
      production:
        domains: [www.storefront.example.com]
        regions:
          us-east-1: { replicas: { min: 2, max: 3 } }
  debug-proxy:
    image: ghcr.io/acme/debug-proxy:1
    regions:
      us-east-1: { cpu: 100m, memory: 128Mi, replicas: { min: 1, max: 1 } }
    environments:
      production:
        enabled: false
```

Commands pick the environment with `--env` or `LOCO_ENV`; a workspace with one environment needs neither, and with several the CLI asks when it runs in a terminal. Select the workspace with `--workspace` or `LOCO_WORKSPACE`, and the organization with `--org` or `LOCO_ORG`. With a user session, each takes a name or an ID. Pass the ID when the CLI reports an ambiguous name. When deploying with an [API token](../operations/access.md#deploy-from-ci-with-an-api-token), pass the workspace ID explicitly.

## Partials

The `partial` key names the slice of the workspace that the file owns. A service belongs to one partial, so several repositories can each manage their own services in one workspace.

- A service in the file that belongs to another partial is a plan error.
- A service in the partial that the file no longer lists is deleted. `services: {}` deletes every service of the partial; a file without a `services` key is invalid.
- A running service that belongs to no partial and appears in the file is imported into the partial.

Move a service to another partial, so that the file declaring that partial owns it from then on:

```sh
loco infra transfer api --to platform
```

## Plan, apply and deploy

```sh
loco infra plan --env staging
loco infra apply --env staging
loco deploy --env staging
```

`plan` prints the operations an apply would perform (create, update, delete, import) and changes nothing. With `--detailed-exit-code` it exits 2 when there are changes, and `--json` prints the plan as JSON. `apply` shows the plan and asks for confirmation; it fails if the environment changed since the plan was shown. Pass `--yes` when there is no terminal. Delete operations need `--confirm-destructive`, and imports need `--confirm-import`.

`loco deploy` applies the file and builds its source services. It packs the directory that holds `loco.yaml` once, leaving out `.git`, `.env`, `.env.*` and anything the `.dockerignore` in that directory excludes except the Dockerfiles it builds, uploads it, and builds each service with its own `dockerfile` and `context`. When the directory is in a git work tree, each build records the commit it is checked out at, its branch and its subject line. A source service the environment does not have yet is created first so it can be built; every other change in the plan is applied with the builds, so a failed build changes nothing else. Image services deploy without a build. Name services to build only those:

```sh
loco deploy web --env production --yes
```

Deploy a prebuilt public image for one service without editing `loco.yaml`:

```sh
loco deploy web --image ghcr.io/acme/web:release --env production --yes
```

`--image` accepts a tag or digest reference. Name exactly one service; the name is optional when the selected environment runs only one service. The override replaces that service's image, Dockerfile and build context for the selected environment, including environment-specific build settings. The API resolves and pins the image before confirmation. It stores the applied image in the service's deployment configuration; the local file stays unchanged, so a later deploy uses the file's original build inputs.

This command skips all source builds and uploads, including builds for other services. It still plans and applies the whole file with the normal import and destructive-operation confirmations. Other services can change, and new source services may still require their initial build. An unknown service, a service disabled in the selected environment, multiple service names or an empty image reference is an error. Image resolution errors stop the deploy before apply.

## Variables and secrets

`env` holds plain values, which appear in the plan and in the file. Configure application trace sampling with standard OpenTelemetry variables under `env`; there is no separate tracing block. See [Trace sampling](../operations/applications.md#trace-sampling) for a portable example and SDK defaults.

A plain value can reference a variable Loco sets, listed in [Runtime environment](../operations/applications.md#runtime-environment), as `$(NAME)`; Kubernetes expands it when the container starts. Use it for software that takes the collector address in its own variables:

```yaml
env:
  OTEL_COLLECTOR_HOST: $(LOCO_OTEL_HOST)
  OTEL_COLLECTOR_PORT_GRPC: $(LOCO_OTEL_GRPC_PORT)
```

`$$(NAME)` produces a literal `$(NAME)`.

`secrets` lists the secret names the service reads; the file never contains their values. A secret belongs to one service in one environment, and `loco env` sets it:

```sh
loco env set --service web --env production STRIPE_API_KEY=sk_live_...
```

The plan reports each listed name the service does not hold in the environment as an error. A value can only be set on a service that exists, so when a new service's only plan errors are its secrets, `loco deploy` and `loco infra apply` create it without building or deploying it, print the `loco env set` command for its secrets, and exit with an error. Run the command again once the values are set.
