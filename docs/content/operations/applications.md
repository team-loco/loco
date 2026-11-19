---
title: "Application operations"
---

The CLI reads application status and manages application resources through the selected Loco API. Check command help for the arguments supported by your release:

```sh
loco resource --help
loco resource status --help
loco resource logs --help
loco resource events --help
```

`loco resource` also includes `scale`, `restart`, `cancel`, and `destroy`; `loco env` manages secrets. Inspect an operation's help before changing resources or removing an application.

`loco resource logs --query` prints only the lines that match a [log query](../reference/log-queries.md), such as `loco resource logs api --query '@level:error -@path:/healthz'`.

## Scale

`loco resource scale` changes a service's replicas, CPU or memory in one environment and rolls out one new deployment to every region the service runs in there, or only to the region `--region` names; the other regions keep their deployment. Regions that run different deployments, such as after a scale of one region, each get a copy of their own deployment. `--env` or `LOCO_ENV` selects the environment; a workspace with a single environment uses it, and a workspace with several fails without one. Other environments keep their deployments. `--replicas` sets a fixed replica count and disables autoscaling; changing only CPU or memory preserves autoscaling. Configure autoscaling per region in [loco.yaml](../deployment/loco-yaml.md). The dashboard's Settings tab scales the environment selected in the breadcrumb.

```sh
loco resource scale web --env production --replicas 3
loco resource scale web --env staging --cpu 250m --memory 512Mi
loco resource scale web --env production --region eu-west-1 --replicas 4
```

## Cancel

`loco resource cancel` stops the newest deployment of a service that is still building or rolling out in one environment; the **Cancel** button on a deployment card in the dashboard does the same for that deployment, and both call `DeploymentService.CancelDeployment`. Canceling stops the build unless another deployment in flight uses it. Each region the deployment does not run yet goes back to the deployment that served it before, or loses its Application when none did. Each region where the deployment already runs keeps it. The deployment ends `canceled` and records who canceled it. A deployment that already runs everywhere, failed or was replaced cannot be canceled; roll back or redeploy it instead.

A new deployment of the service replaces one in flight without a cancel: the older deployment ends `canceled`, and its card names the deployment that replaced it. `--wait` exits with an error naming who canceled a deployment, and succeeds with a note when a newer deployment replaced it.

```sh
loco resource cancel web --env production
```

## Deployments and regions

A deployment is one request to run a version of a service in an environment: a `loco deploy`, a `loco infra apply`, a scale, a redeploy or a rollback. It rolls out to every enabled region of the service, or to the regions the request names, with one placement per region. Each placement has its own status, desired replicas and image, and the deployment's status follows them:

- `building` while the build runs, then `deploying` while any region rolls out.
- `failed` as soon as one region fails; regions that already run keep the new version unless [rollback on failure](../deployment/loco-yaml.md#rollback-on-failure) is on.
- `running` once every region runs it, and `superseded` once newer deployments replace it in every region.
- `canceled` when someone [cancels](#cancel) it, or when a newer deployment replaced it before it ran anywhere.

A newer deployment never waits for one in flight. It cancels the older deployment's regions that have not started, and replaces the ones that run once its own replicas are ready there, so the previous version serves until then. `CreateDeployment` takes `regions` (empty means every enabled region) and, for a redeploy or rollback, `trigger` and `source_deployment_id` naming the deployment it copies.

## Restart

`loco resource restart` starts a rolling restart of the current replicas in one environment, preserving the deployment spec and ID. It uses the same `--env` and `LOCO_ENV` selection as scale. Without `--region`, it restarts every region in that environment. Restart requires write access. A restart during a rollout rolls the replicas again.

```sh
loco resource restart web --env production
loco resource restart web --env staging --region us-east-1
```

The dashboard's **Restart all replicas** action restarts the environment selected in the breadcrumb. The event history records the request as `resource.restarted`.

## Secrets

`loco env` manages the secrets of one service in one environment. `--service` names the service and is required; `--env` or `LOCO_ENV` selects the environment. Secrets belong to that service only, so two services that need the same value each get it set. `push` sends every `KEY=VALUE` line of a `.env` file (or stdin) in one call, `set` takes assignments on the command line, `unset` removes names and `list` shows names, versions and who set them. No command prints a value, and `$NAME` in a `.env` file stays literal. Saving secrets leaves running replicas unchanged, and new values apply on the next deploy or restart. When an active deployment uses a name whose value changed, the CLI and the dashboard name the secrets and regions, and the CLI prints the `loco resource restart` command. `unset` refuses a name bound by an active deployment. Remove the binding from the deployment first.

```sh
loco env push --service web --env production .env
loco env set --service web --env production API_KEY=abc123
loco env unset --service web --env production API_KEY
loco env list --service web --env production
```

## Container requirements

Applications run under Kubernetes' `restricted` Pod Security profile. Use an image with a numeric non-root `USER`, or set `runAsUser` in `loco.yaml` to a positive numeric UID compatible with the image. Set `runAsGroup` when it also needs an explicit primary GID; see [Runtime user](../deployment/loco-yaml.md#runtime-user). The pod sets the `net.ipv4.ip_unprivileged_port_start=0` sysctl, so the process can listen on any port from 1 to 65535 without root or extra capabilities.

## Runtime environment

By default, the controller sets these variables in every application container:

| Variable | Value |
|---|---|
| `LOCO_APP_NAME`, `LOCO_RESOURCE_ID`, `LOCO_WORKSPACE_ID`, `LOCO_DEPLOYMENT_ID` | the application's identifiers |
| `LOCO_REGION`, `LOCO_ENVIRONMENT` | the region and environment the deployment targets |
| `LOCO_INTERNAL_DOMAIN`, `LOCO_PUBLIC_DOMAIN` | the in-cluster hostname, and the public hostname or an empty string |

The `LOCO_*` variables cannot be overridden. Automatic application SDK environment configuration is enabled by default. Set `observability.injectOtel: false` in `loco.yaml`, or turn off **Configure SDK environment** in the resource's Observability settings, to manage SDK configuration yourself.

When automatic environment configuration is enabled, Loco supplies these variables to instrumented applications whose SDK supports environment configuration:

| Variable | Value |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL` | the region's OpenTelemetry collector over OTLP/HTTP (`http/protobuf`) |
| `OTEL_SERVICE_NAME` | the resource's name, such as `checkout` |
| `OTEL_RESOURCE_ATTRIBUTES` | `deployment.environment.name` and `loco.deployment.id` |

A plain environment variable or declared secret with the same name replaces that one default; Loco still injects the other three. Other OpenTelemetry variables, such as signal-specific exporter settings like `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE`, `OTEL_LOG_LEVEL` and `OTEL_TRACES_SAMPLER`, leave all four defaults in place. Plain values take precedence when the same name also appears in `secrets`.

Setting `injectOtel` to `false` removes the injected set on rollout and preserves user-supplied variables and secrets. It does not disable an SDK configured by the application or its image, or stop Loco from collecting telemetry sent to it. Loco does not inspect configuration inside the application or its image.

For data sent to Loco, the collector attaches workspace, environment and resource IDs from pod labels and drops application-supplied values for those IDs.

The collector preserves application-provided service identity. It does not infer `service.name`, `service.namespace`, `service.version` or `service.instance.id` from Kubernetes labels, workload names, namespaces or container images. Kubernetes identity remains available under the `k8s.*` attributes.

Each deployment records the plain environment variables it runs with. A redeploy or rollback runs the variables and binds the secret names of the deployment it copies, and a canceled deployment's regions go back to the variables of the deployment that served them before. In `CreateDeployment`, the spec's environment map is the full set of plain variables; an empty map runs none. Omit `secret_bindings` to keep the secret names of the copied deployment, or for a deploy the current ones. Supply `secret_bindings.names` to select secrets already stored for the service in the target environment, including on the first deployment or in a new region. An empty list clears the bindings.

The **Variables** section of a resource's settings shows which stored secrets the active deployment reads. For a service deployed from the dashboard, a switch on each secret selects whether deployments read it, and a secret added there is selected by default. The same section adds, edits and removes plain variables. Saving a change to either redeploys every region of the environment with the new `secret_bindings` and environment map. Removing a stored secret that deployments read redeploys without it, then deletes it, in one save. When regions of the environment run different variables, the section says so, and saving applies the list it shows to every region. For a service deployed from `loco.yaml`, the file's `secrets` list selects the names, and each stored secret it does not list shows the `services.<name>.secrets` entry to add.

### Trace sampling

Loco leaves application trace sampling to the SDK. Set the standard OpenTelemetry variables under the service's `env` map to sample 10% of new root traces while respecting a parent's sampling decision:

```yaml
services:
  web:
    env:
      OTEL_TRACES_SAMPLER: parentbased_traceidratio
      OTEL_TRACES_SAMPLER_ARG: "0.1"
```

These settings work outside Loco with an SDK that supports them. When they are unset, the application's SDK defaults apply. The [OpenTelemetry environment specification](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/#general-sdk-configuration) specifies `parentbased_always_on`: sample new root traces and follow the parent's decision for child spans. Check your SDK's configuration support and any sampler set in application code. To drop new root traces while following sampled incoming traces, set `OTEL_TRACES_SAMPLER: parentbased_always_off`.

`loco.yaml` has no tracing configuration block. Application sampling is independent of the self-hosted gateway's `envoyProxy.tracing.samplingRate`, which controls ingress sampling.


## Collected log levels

Container stdout and stderr records keep their original message. When a record has no severity, the collector extracts a string `level`, `severity` or `severityText` from a JSON object, a leading level such as `[WARNING]`, or a `level=warn` field. An ISO timestamp can precede a leading level; terminal color codes do not affect detection. `warning` becomes `WARN` and a leading `panic` becomes `FATAL`; trace, debug, info, error and fatal levels are also recognized, without regard to case. Existing severity is preserved.

The collector drops empty lines and joins a multi-line entry into one record: lines that start with whitespace, Java `Caused by:` lines and the lines of a Go panic or goroutine trace continue the line before them. A Go panic therefore arrives as one `FATAL` record that holds its whole stack trace.

Malformed JSON, unknown levels and messages without a recognized level remain unclassified and appear as INFO in the dashboard. A word such as “warning” inside an ordinary message does not set its severity. Structured OTLP logs retain the severity supplied by the application SDK. When a replica exports logs over OTLP, the logs view shows those records and hides the replica's stdout lines, which repeat them; its stderr lines, where crashes are printed, stay visible.

## Public, private and worker applications

An application with a domain is public: the gateway routes its hostname to the application. An application with a `port` and without a domain is private: it accepts connections only from inside its workspace, through its [private hostname](../deployment/loco-yaml.md#private-service-discovery). To deploy a private application, leave `domains` off the service in `loco.yaml`:

```yaml
services:
  cart:
    image: ghcr.io/acme/cart:2.1.0
    port: 7070
    regions:
      us-east-1: { cpu: 100m, memory: 256Mi, replicas: { min: 1, max: 1 } }
```

An application without a `port` is a worker, such as a queue consumer: it runs, scales and writes logs like any other and makes outbound connections, but it has no hostname, takes no traffic and gets no readiness probe. Leave out `port` to deploy one:

```yaml
services:
  worker:
    image: ghcr.io/acme/worker:1.4.2
    regions:
      us-east-1: { cpu: 100m, memory: 256Mi, replicas: { min: 1, max: 1 } }
```

`loco deploy` prints that a private application has no public URL and that a worker takes no traffic, and `loco resource status` shows `URL: none` for both. In the dashboard, the New service drawer on the Resources page offers the same choice with its Public switch. Its Port field starts empty, which creates a worker. A domain added to an existing application takes effect on its next deployment, and removing an application's last domain makes it private from its next deployment.

## Investigate a rollout

Use application status to find whether a deployment is progressing or ready. `loco resource status` shows a status for each environment the application runs in, and for each region its ready and desired replicas, its deployment and the reason a region is not ready:

- Healthy: every desired replica is ready.
- Deploying: a first rollout has no ready replica yet.
- Degraded: some replicas or regions are not ready.
- Unavailable: no replica is ready.
- Unknown: the cluster running a region has missed three heartbeats, or reports that it is unhealthy, so its last report is not trusted. The agent sends its heartbeats on the same stream as its replica reports, at the interval the API sets with `LOCO_AGENT_HEARTBEAT_INTERVAL` (30 seconds by default), so a region goes Unknown 90 seconds after that stream drops. Loco places no new deployments on such a cluster until it heartbeats again.

A replacement keeps the previous running deployment running until the replacement is fully available. The previous deployment then becomes superseded. If the replacement fails, the previous deployment stays running. Replacing a deployment that has never run cancels it. A restart returns a deployment to deploying until its new revision is available.

Below each environment, `loco resource status` lists every replica of each region: its state, restarts, last exit (such as `OOMKilled (137) 5m ago`), age and node. A replica that restarted within the service's restart window and is not serving reads `Restarting (2/5)`: its restarts in the window against the budget. While a region reads Unknown, the list keeps the cluster's last report and ends with when it was sent. `--output json` includes the same replicas, with each replica's start time and last exit reason, exit code and finish time.

The controller reports each replica's state and the rollout's current problem, availability and progress deadline. Replica states include scheduling, starting, serving, failing and shutdown outcomes. A problem carries one reason, a fixed message and specific diagnostic detail. Region messages and runtime notices use that problem. Reconciliation continues after a failure, so the application can recover when replicas become ready.

The status comes from the replicas the cluster reports ready, not from the deployment record. A rollout stops no previous replica before a new one has stayed ready for the worker cluster's `controller.minReadySeconds` (10 seconds by default), so a redeploy whose new replicas fail leaves the application Healthy while every previous replica still serves, and the region shows the failure. Public routes and private port 80 continue reaching each ready replica on its own application port during port changes. With automatic rollback off, the failed deployment remains the region's current deployment. See [Rollback on failure](../deployment/loco-yaml.md#rollback-on-failure). Read Loco events for runtime problems, then inspect logs for application startup failures. The dashboard provides another view of the same applications.

The dashboard shows **Desired deployment** separately from **Serving deployments**. The serving list counts the ready, non-terminating replicas in each cluster's latest accepted report and identifies each replica's own deployment. An older deployment can keep serving while a new build is pending, after a failed rollout, or while a retiring cluster finishes deleting its workloads. Several deployments can serve during a rollout. Until a cluster reports the new revision, the dashboard keeps its last reported serving identities. Suspension clears those identities until a fresh report arrives after resumption. A replacement placement starts without a serving report. Historical deployment details keep their historical replica snapshots. Deployment-history and build-image retention preserve the last reported deployment context and versions with live replicas, including replicas that may become ready later. Those versions become eligible for cleanup after a newer report replaces them or workload deletion is acknowledged.

During a successful rollout, Kubernetes delays signaling a retiring service container by the worker chart's `controller.drainSeconds` (5 seconds by default). This native preStop sleep lets routing withdraw the endpoint while the process still accepts traffic. The process then retains Kubernetes' normal 30-second shutdown budget. Portless workers have no added delay. Increase the cluster's drain window when routing needs longer; a stalled control plane or routing update can outlast it. The hook applies to replacement Pods, so changing the controller setting rolls serving Pod templates and cannot protect old Pods retroactively.

`loco resource events <name>` lists Loco events, newest first. Use `--limit 20` to limit the output, `--query` to keep the events that match an [audit query](../reference/audit-queries.md), such as `--query '@type:resource.*'`, or `--output json` for event IDs, creation times and structured data. The event history records transitions: `resource.degraded` when a runtime problem changes, and `resource.recovered` when it clears. Repeated reports of the same problem do not add events. A replica that restarted keeps its `restarting` or `out_of_memory` problem until it has gone the worker cluster's `controller.healthyAfterSeconds` (5 minutes by default) without another restart. A replica that restarts more often than its service's [restart policy](../deployment/loco-yaml.md#restarts) allows, 5 times in 10 minutes by default, stops its revision with `restarts_exhausted`. The previous version takes its place, and the deployment fails, when its replicas are still running or when it proved itself by serving on every replica for the health window without a restart. Otherwise, including a version restarted after it stopped, the service runs no replicas and its deployment becomes `crashed` until `loco resource restart`, a redeploy or a rollback starts it again with a fresh budget. A running deployment whose replica restarted or stopped being ready is `degraded` until the controller reports it healthy again.

Each restart adds an activity entry that names its cause and the budget, such as `Restarted after exit 1 · 2 of 5 in 10 min`, `Restarted after failing its health check · 3 of 5 in 10 min` or `Restarted after running out of memory · 1 of 5 in 10 min`. The kubelet records the liveness kill as an event that can arrive after the restart, so an entry written before it arrives names the exit instead.

Runtime events use the deployment's problem reason, such as `restarting`, `restarts_exhausted`, `out_of_memory`, `image_pull_failed`, `unschedulable` or `health_check_failing`. Events include a Loco-written message, placement ID and region in their data, with organization, workspace, resource and environment IDs for filtering. The dashboard's events drawer and warning count use this history; its restarts card shows recent logs for the affected resource, environment and region. Kubernetes reasons and replica names are not included in events.

`EventService.ListEvents` reads one organization, workspace or resource, optionally one environment and a creation time range, and its `query` field selects events by actor, target, event type and request ID with an [audit query](../reference/audit-queries.md). The dashboard's audit log search box sends its text as that query, so it searches the whole log.

```sh
loco web
```

To define services and their domains, see [loco.yaml](../deployment/loco-yaml.md).
