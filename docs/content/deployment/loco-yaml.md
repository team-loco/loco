---
title: "loco.yaml"
---

`loco.yaml` declares the services of a workspace: what to run, where, and how much. The CLI sends the file to the API, which plans the difference against an environment and applies it. The file is optional for a single service: `loco deploy` can deploy from the current directory with API defaults, as a worker, or with `--port` as a service with a public hostname formed from the safe directory name and a four-letter suffix.

## Create a file

```sh
loco infra init --name storefront
```

`init` writes a starter `loco.yaml` from the API's [service defaults](../operations/self-hosting.md#installation-inputs): CPU, memory and replica counts and the default region. Nothing defaults a port, so the starter service is a worker; add `port` and `domains` to serve traffic. The first line of the file points editors at the JSON Schema URL the API reports, `https://loco.build/schemas/loco.v1.json` on Loco's hosted service, which gives completion and inline errors. The dashboard serves the schema at `/schemas/loco.v1.json`.

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

`port` is the port the service listens on, and nothing fills in a default. A service without `port`, such as `worker` above, is a worker: Loco creates no Kubernetes Service, route or readiness probe for it, so it needs no listener and cannot have `domains`, `routing` or `health`. The plan rejects those fields on a service without `port`. Each new replica of a worker counts as ready once it has run for the worker cluster's `controller.minReadySeconds`; see [health checks](#health-checks).

A service builds from source or runs a public `image`, never both. A source service builds in its `context`, a directory relative to `loco.yaml` that defaults to `.`, from its `dockerfile`, a path relative to `context` that defaults to `Dockerfile`. This service builds from `services/web/deploy/Dockerfile` with `services/web` as the build context:

```yaml
services:
  web:
    context: services/web
    dockerfile: deploy/Dockerfile
```

A service with `port` and without `domains` is private. Each entry of `domains` is one label under a platform domain, such as `storefront.example.com` under `example.com`. A platform domain is one a cluster serves: the cluster's agent reports it once the Gateway listeners for `*.<domain>` are accepted and programmed and the certificate covering it is ready. The plan rejects a hostname unless the cluster of every region the service runs in serves its platform domain, and names the clusters that do not. When a cluster stops serving a domain, assigned hostnames stay, and the region's status turns degraded with the hostnames it no longer serves. Each region needs `cpu` from `100m` to `2000m`, `memory` from `32Mi` to `4Gi`, and `replicas.min` and `replicas.max` from 1 to 10. Without `autoscaling`, the service runs `replicas.min` replicas. Set `autoscaling: { cpuTarget: 70 }` or `autoscaling: { memoryTarget: 80 }` on a region to scale between `replicas.min` and `replicas.max`. The target is a percentage of the container’s resource request, from 1 to 100. `loco.yaml` accepts exactly one target per region. Removing `autoscaling` returns the service to `replicas.min` replicas. Partial names and service keys are DNS labels: 1 to 63 lowercase letters, digits and hyphens, starting with a letter and ending with a letter or digit. The API checks resource limits when it plans the file, and `GetConfig` reports them as `size_limits`. The API accepts any Kubernetes quantity within them. The JSON Schema accepts CPU as whole millicores such as `250m` or as cores such as `0.5`, and memory as whole mebibytes such as `512Mi` or as gibibytes from `1Gi`, such as `1.5Gi`.

## Private service discovery

Applications in the same workspace on the same cluster reach services that declare `port` through native Kubernetes DNS, such as `http://cart.loco-ws-00000000-0000-7000-8000-000000000003.svc.cluster.local`. Port 80 forwards to the configured application port, so callers omit the port even when cart listens on 7070. Loco uses namespaces named `loco-ws-<environment UUID>`; replace the example UUID with the target environment’s UUID. A caller in that namespace can also use `http://cart`. Both private and publicly routed services have these endpoints. Callers need no resource IDs, OIDC tokens or discovery API calls. Declare dependent services in regions where they run on the same cluster; these DNS names do not connect separate clusters or regions.

For example, a caller can pass dependency addresses through its environment:

```yaml
services:
  checkout:
    env:
      CART_URL: http://cart.loco-ws-00000000-0000-7000-8000-000000000003.svc.cluster.local
      PAYMENT_ADDRESS: payment.loco-ws-00000000-0000-7000-8000-000000000003.svc.cluster.local:50051
```

Use the cluster's configured DNS suffix in place of `cluster.local` when it has a custom cluster domain. Namespace-qualified names distinguish a service’s environments and separate identical service names in different workspaces. Loco uses the cluster's DNS. Namespace-qualified names identify Services; network policies control which callers can reach them.

The operator creates a ClusterIP Service named after the resource. It exposes TCP port 80 and the configured application port, both forwarding to each ready replica's application port. When the application uses port 80, the Service exposes it once. gRPC and other TCP clients can use the configured port or port 80 explicitly; their default port depends on the client protocol. A non-HTTP service can omit `health` to avoid HTTP probes. Short names such as `cart:7070`, the `resource-<UUID>:80` endpoint and public HTTP routes also reach the service.

A resource has a name and a slug. The name is free text of 1 to 63 characters, such as `QA Echo`, which the dashboard shows. The slug is a DNS label, such as `qa-echo`, that `loco.yaml` service keys, CLI arguments, `@resource:` filters and the private hostname use. The dashboard derives the slug from the name, dropping accents, and does not edit it. Names are unique within a workspace regardless of case, and so are slugs. A service that `loco.yaml` creates takes its key as both name and slug. Changing the name in the resource's settings leaves the slug alone. The slug of a resource that no `loco.yaml` manages can change through `ResourceService.UpdateResource`. Callers of the old private hostname, CLI scripts and saved filters that use the old slug stop finding it, and every replica in every environment and region restarts to pick up the new slug. The slug of a resource that `loco.yaml` manages is its key in the file, and `UpdateResource` refuses to change it. A slug starts with a letter, so it is also a valid Service name on every supported Kubernetes version. The slugs `localhost` and `resource-<UUID>` are reserved.

Port changes update the logical endpoint while preserving its ClusterIP. Port 80 keeps reaching ready replicas on their own application ports while old and new replicas overlap. The additional port follows the latest configured application port, so callers that must survive a port change use port 80. Resource deletion removes its aliases. Replacing a resource with another of the same name waits for the previous resource to release it. An unmanaged Service or a Service owned by another Application causes reconciliation to fail; the operator does not adopt or overwrite it.

Each environment has its own namespace, so the same service can run independently in several environments on one worker cluster. A short name such as `cart` reaches the caller’s own environment; a namespace-qualified name reaches the named environment. The workspace network policies allow communication across that workspace’s environment namespaces. Other workspaces remain isolated.

A workspace's namespaces on a worker cluster exist only while the workspace has deployments there, because the controller creates a namespace when it reconciles a deployment and a namespace always has a placement behind it. When a workspace has had no deployment on a cluster for `LOCO_WORKSPACE_NAMESPACE_GRACE` (24 hours by default), the API asks that cluster's agent to delete the workspace's namespaces there, including the pull secret and network policies. A new deployment recreates them.

## Runtime user

Set `runAsUser` and `runAsGroup` to positive numeric IDs when an image uses a named user that Kubernetes cannot verify as non-root:

```yaml
services:
  accounting:
    runAsUser: 1654
    runAsGroup: 1654
```

These fields override the image's UID and primary GID independently. Omitted fields use the image defaults; an environment override can replace an ID or remove it with `null`. IDs must be between 1 and 2147483647. Loco continues to enforce non-root execution, disabled privilege escalation, dropped capabilities and the runtime's default seccomp profile.

Choose IDs that can read the application and write its working directories. The override does not create users, change file ownership or make an application that requires root compatible. Changes roll out new pods without rebuilding the image.

## Command and arguments

`command` and `args` change how the container starts. `command` replaces the image's `ENTRYPOINT` and `args` replaces its `CMD`, as in a [Kubernetes container](https://kubernetes.io/docs/tasks/inject-data-application/define-command-argument-container/). This service runs the image's entrypoint, `/http-echo`, with two flags:

```yaml
services:
  echo:
    image: hashicorp/http-echo:1.0
    port: 8080
    args: ["-listen=:8080", "-text=hello world"]
```

Each field is a list of strings, one entry per word. Loco runs the program directly, without a shell, so quotes and spaces inside an entry reach it unchanged: `-text=hello world` above is one argument. To use a shell, make it the command: `command: [/bin/sh, -c]` and `args: ["exec myapp --port $PORT"]`.

| Set | The container runs |
| --- | --- |
| Neither | The image's `ENTRYPOINT` with its `CMD` |
| `args` | The image's `ENTRYPOINT` with `args` |
| `command` | `command` alone; the image's `CMD` is dropped |
| Both | `command` with `args` |

Kubernetes replaces `$(NAME)` in either field with the value of the container's environment variable `NAME`, and `$$(NAME)` becomes the literal text `$(NAME)`. Each field takes at most 100 entries, and a `command` entry cannot be empty. An environment override replaces a whole list, and `null` removes it. A change rolls out a new deployment without rebuilding the image, and `loco infra plan` shows each list with its entries quoted.

## Health checks

Omit `health` for a TCP readiness check: a replica is ready once it accepts connections on `port`, Loco creates no liveness probe, and Kubernetes restarts exited containers. This suits gRPC and other TCP services without an HTTP health endpoint; use protocol-specific E2E checks to verify that it can serve requests. A service that never listens, such as a queue worker, leaves out `port`: it gets no probe, and a replica is ready once it has started.

Set `health: {}` to enable HTTP liveness and readiness checks with the API defaults, or configure `path`, `interval`, `timeout`, `failThreshold` and `startupGracePeriod`. The `timeout` must be less than the `interval`. An environment override with `health: null` removes an inherited check. Removing `health` from an existing service redeploys it with the TCP readiness check.

A rollout starts new replicas next to the old ones, up to a quarter more than the desired count, and stops an old replica only after a new one has stayed ready for the worker cluster's `controller.minReadySeconds` (10 seconds by default). A new version that crashes, never accepts connections or fails its health check therefore receives no traffic and replaces no old replica: the deployment fails and the previous version keeps serving with all its replicas. A worker's new replica replaces an old one once it has run that long without exiting. Before stopping an old service replica, Kubernetes waits the worker cluster's `controller.drainSeconds` (5 seconds by default) for routing to withdraw its endpoint, then gives the process its normal shutdown budget. This bounded window cannot guarantee routing convergence during a control-plane stall. Portless workers get no drain delay.

Check the file without calling the API. `validate` parses the file and checks it against the JSON Schema built into the CLI, including the CPU, memory and replica limits, and prints each error with its field path. The plan checks what needs the API, such as regions and domains:

```sh
loco infra validate
```

## Failure policy

`deploy` sets when Loco stops a version of the service: after too many restarts, or when a new version does not become healthy in time.

```yaml
services:
  web:
    deploy: { restart: on-failure, maxRestarts: 3, timeout: 300 }
```

Kubernetes starts a replica's process again when it exits, with any exit code, runs out of memory or fails its liveness check. `restart` decides which of those restarts are too many:

- `on-failure` (the default): the restart after `maxRestarts`, between 1 and 5. A replica's count starts again once it stays up for the worker cluster's `controller.healthyAfterSeconds` (300 by default).
- `never`: a replica's first exit.
- `always`: none. `maxRestarts` applies only to `on-failure`.

Too many restarts stop a new version only until it proves itself by serving on every replica for the health window without a restart, and only when stopping it leaves something serving: a new version that has been healthy and has no earlier version to fall back to keeps running. Otherwise a replica that restarts too often keeps running and restarting, and the deployment shows `crashed` until the replica stays up for the health window again. A healthy version that has not proved itself can still be stopped well after its rollout, for example when one replica uses up its restarts 40 minutes in; its earlier proven version then serves again.

`timeout`, between 60 and 3600 seconds, is how long a new version has to become healthy: every replica available, and none restarted within the health window. A version that is still unhealthy at its timeout stops, whatever held it back: replicas that cannot be scheduled, an image that cannot be pulled, a health check that never passes or a crash loop. The timeout starts when the cluster first sees the version and ends once it is healthy, so a replica added later by scaling is not held to it.

The dashboard sets the same fields under **Deployment behavior** in a service's settings; a saved change applies from the service's next deployment without redeploying it. The dashboard stores every field, so a file whose `deploy` leaves them out plans their removal. An omitted field uses the API default: `on-failure` with 2 restarts and a 600-second timeout, unless the operator sets `LOCO_DEFAULT_MAX_RESTARTS` or `LOCO_DEFAULT_DEPLOY_TIMEOUT`. `GetConfig` reports the defaults as `failure_policy`.

A stopped version runs no replicas. The previous version takes its place when its replicas still run, or when it proved itself by serving on every replica for the health window without a restart; the deployment then fails, and remains the region's current deployment until the next one. A version that misses its timeout with nothing to fall back to also fails. A first version stopped for restarts before it was ever healthy runs nothing, and its deployment becomes `crashed` until `loco resource restart`, a redeploy or a rollback starts it again. A running deployment whose replica restarted is `degraded` and returns to `running` once every replica has gone the health window without a restart.

`deploy` applies to the service in every environment, so an environment override cannot set it. A change reaches the cluster with the service's next deployment. When `deploy` is the only change, `loco infra apply` updates the service without redeploying it; `loco deploy` still builds and redeploys the services it targets.

When a cluster's agent cannot apply a deployment and will not retry it itself, for example because Kubernetes rejects the Application, the API sends the deployment again after `LOCO_APPLY_RETRY_DELAY` seconds (300 by default) and marks it failed after `LOCO_APPLY_ATTEMPTS` tries (2 by default, at most 10).

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

`loco deploy` applies the file and builds its source services. It packs the directory that holds `loco.yaml` once, leaving out `.git`, `.env`, `.env.*` and anything the `.dockerignore` in that directory excludes except the Dockerfiles it builds, uploads it, and builds each service with its own `dockerfile` and `context`. When the directory is in a git work tree, each build records the commit it is checked out at, its branch and its subject line. A source service the environment does not have yet is created first so it can be built; the rest of the plan is applied as soon as the builds are queued. Each source deployment starts in `building` without changing its cluster placement. A successful build starts its rollout; a failed build fails its deployment with the build diagnostic; a canceled build cancels its deployment. Image services deploy without a build. Name services to build only those:

```sh
loco deploy web --env production --yes
```

Deploy a prebuilt public image for one service without editing `loco.yaml`:

```sh
loco deploy web --image ghcr.io/acme/web:release --env production --yes
```

`--image` accepts a tag or digest reference. Name exactly one service; the name is optional when the selected environment runs only one service. The override replaces that service's image, Dockerfile and build context for the selected environment, including environment-specific build settings. The API resolves and pins the image before confirmation. It stores the applied image in the service's deployment configuration; the local file stays unchanged, so a later deploy uses the file's original build inputs.

This command skips all source builds and uploads, including builds for other services. It still plans and applies the whole file with the normal import and destructive-operation confirmations. Other services can change, and new source services may still require their initial build. An unknown service, a service disabled in the selected environment, multiple service names or an empty image reference is an error. Image resolution errors stop the deploy before apply.

`apply` and `deploy` return once the API records the deployments they start; the cluster rolls them out afterwards. With `--wait` they return once every one of those deployments runs, meaning its replicas run the new configuration and are ready, and print each activity entry as it arrives, including source fetching, image building, image pushing and rollout progress, prefixed with its region, plus a `<region> · <state>` line each time a region's state changes, followed by a success line for each deployment. A dropped stream resumes after the last printed entry. The command exits with the problem message and detail of each failed region when a deployment fails, or is canceled because its service stopped or was deleted during the rollout, so a script or CI job can stop on a failed rollout:

```sh
loco deploy --env production --yes --wait
```

## Observability

Loco configures instrumented applications to export telemetry to its collector by default. Disable automatic SDK environment configuration for a resource or an environment:

```yaml
services:
  checkout:
    observability:
      injectOtel: true
    environments:
      staging:
        observability:
          injectOtel: false
```

`injectOtel` defaults to `true`. Loco supplies `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES`. A variable with one of those names replaces that default, and Loco injects the rest. Other OpenTelemetry variables, such as `OTEL_LOG_LEVEL`, sampler settings or signal-specific exporter settings, leave the defaults in place. See [Runtime environment](../operations/applications.md#runtime-environment) for the supplied values.

Application code must already include the SDK and instrumentation. Setting `injectOtel` to `false` leaves SDK configuration to the application; it does not disable telemetry collection. Environment overrides replace the resource's injection preference. Saving the setting creates a deployment snapshot and rolls out the resource.

## Variables and secrets

`env` holds the service's variables, which appear in the plan and in the file. Configure application trace sampling with standard OpenTelemetry variables under `env`; there is no separate tracing block. See [Trace sampling](../operations/applications.md#trace-sampling) for a portable example and SDK defaults.

A plain value can reference a variable Loco sets, listed in [Runtime environment](../operations/applications.md#runtime-environment), as `$(NAME)`; Kubernetes expands it when the container starts:

```yaml
env:
  DEPLOYMENT_REVISION: $(LOCO_DEPLOYMENT_ID)
```

`$$(NAME)` produces a literal `$(NAME)`.

A value can also refer to a secret of the environment as `${{ secrets.NAME }}`, or to an output of another service in the workspace as `${{ service.KEY }}`. The file never contains a secret's value. A secret belongs to one environment, `loco secret` sets it, and any service in that environment can read it:

```sh
loco secret set --env production STRIPE_API_KEY=sk_live_...
```

```yaml
services:
  api:
    port: 8080
    env:
      DATABASE_URL: "${{ secrets.DATABASE_URL }}"
  web:
    port: 3000
    env:
      API_URL: "${{ api.PRIVATE_URL }}"
      STRIPE_API_KEY: "${{ secrets.STRIPE_API_KEY }}"
```

A reference can sit inside a longer value, such as `postgres://app:${{ secrets.DB_PASSWORD }}@db:5432/app`. Every service provides these outputs:

| Output | Value |
|---|---|
| `PRIVATE_HOST` | the service's slug, such as `api` |
| `PORT` | its `port`; only a service with `port` provides it |
| `PRIVATE_URL` | `http://<slug>:<port>`; only a service with `port` provides it |
| `PUBLIC_URL` | `https://` and its primary domain, the first entry of its `domains`; only a service with a domain provides it |

A reference names a service by its slug, the key it has under `services`. Any other key names one of that service's variables, such as `${{ api.LOG_LEVEL }}`, which can itself refer to secrets or other services. A reference to a service of the same file resolves against the file, and a reference to any other service against that service's latest deployment in the environment. A secret value reaches the container only; the plan and the deployment record keep the reference. The slug `secrets` is reserved for references to secrets. There is no escape for `${{`: every `${{ … }}` in a value is a reference. Deploying from the dashboard or `CreateDeployment` needs read access to each service a variable refers to directly, not to the services those refer to in turn, and read access to the workspace when a variable reads a secret.

The plan reports each reference it cannot resolve as an error on `env.NAME`: a secret the environment does not have, a service the workspace does not have, a key the service does not provide, a reference that is not of the form `${{ source.KEY }}`, or references that form a cycle. A service reads a changed secret value on its next deploy or restart. `loco secret unset` refuses a secret that the latest deployment of a service reads, and names those services.
