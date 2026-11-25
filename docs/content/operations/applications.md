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

`loco resource` also includes `scale`, `restart`, `cancel`, and `destroy`; `loco secret` manages an environment's secrets. Inspect an operation's help before changing resources or removing an application.

`loco resource logs --query` prints only the lines that match a [log query](../reference/log-queries.md), such as `loco resource logs api --query '@level:error -@path:/healthz'`.

## Scale

`loco resource scale` changes a service's replicas, CPU or memory in one environment and rolls out one new deployment to every region the service runs in there, or only to the region `--region` names; the other regions keep their deployment. Regions that run different deployments, such as after a scale of one region, each get a copy of their own deployment. `--env` or `LOCO_ENV` selects the environment; a workspace with a single environment uses it, and a workspace with several fails without one. Other environments keep their deployments. `--replicas` sets a fixed replica count and disables autoscaling; changing only CPU or memory preserves autoscaling. Configure autoscaling per region in [loco.yaml](../deployment/loco-yaml.md). The dashboard's Settings tab scales the environment selected in the breadcrumb.

```sh
loco resource scale web --env production --replicas 3
loco resource scale web --env staging --cpu 250m --memory 512Mi
loco resource scale web --env production --region us-va-1 --replicas 4
```

## Cancel

`loco resource cancel` stops the newest deployment of a service that is still building or rolling out in one environment, or that shows `running` or `degraded` without ever having had every replica available; the **Cancel** button on a deployment card in the dashboard does the same for that deployment, and both call `DeploymentService.CancelDeployment`. Canceling stops the build unless another deployment in flight uses it. Each region where the deployment has not yet had every replica available goes back to the deployment that served it before, or loses its Application when none did, even if the region already shows it `running`. Each region where it has keeps it, unless you also roll those regions back. The deployment ends `canceled` and records who canceled it. A deployment that was available everywhere, failed or was replaced cannot be canceled; roll back or redeploy it instead.

A new deployment of the service replaces one in flight without a cancel: the older deployment ends `canceled`, and its card names the deployment that replaced it. `--wait` exits with an error naming who canceled a deployment, and succeeds with a note when a newer deployment replaced it.

When the deployment already runs in some regions, the dashboard's cancel dialog lists those regions with the version each ran before and when it was deployed, and offers an unchecked option to roll them back. A region that never ran an earlier version keeps the canceled one. A rollback is never automatic, and `loco resource cancel` never rolls back. Rolling back queues one deployment per earlier version to just those regions (`CancelDeploymentRequest.rollback_finished_regions`). Each is an ordinary rollback in the deployment history and can itself be canceled. `DeploymentService.GetDeploymentCancelImpact` returns the regions and versions the dialog shows.

```sh
loco resource cancel web --env production
```

## Deployments and regions

A deployment is one request to run a version of a service in an environment: a `loco deploy`, a `loco infra apply`, a scale, a redeploy or a rollback. It rolls out to every enabled region of the service, or to the regions the request names, with one placement per region. Each placement has its own status, desired replicas and image, and the deployment's status follows them:

- `building` while the build runs, then `deploying` while any region rolls out.
- `failed` as soon as one region fails; regions that already run keep the new version. In a region that fails, its [failure policy](../deployment/loco-yaml.md#failure-policy) stops the new version and the previous one keeps serving.
- `running` once every region runs it, and `superseded` once newer deployments replace it in every region.
- `canceled` when someone [cancels](#cancel) it, or when a newer deployment replaced it before it ran anywhere.

A newer deployment never waits for one in flight. It cancels the older deployment's regions that have not started, and replaces the ones that run once its own replicas are ready there, so the previous version serves until then. `CreateDeployment` takes `regions` (empty means every enabled region) and, for a redeploy or rollback, `trigger` and `source_deployment_id` naming the deployment it copies. A spec without `health_check` runs the resource's own health check; `remove_health_check` runs none and removes the resource's, so later deployments without one run none either. The resource's check is the default for every environment of the workspace: removing it in one environment also removes it from the next deployment in another, while deployments already running there keep their check.

## Restart

`loco resource restart` starts a rolling restart of the current replicas in one environment, preserving the deployment spec and ID. It uses the same `--env` and `LOCO_ENV` selection as scale. Without `--region`, it restarts every region in that environment. Restart requires write access. A restart during a rollout rolls the replicas again.

```sh
loco resource restart web --env production
loco resource restart web --env staging --region us-ca-1
```

The dashboard's **Restart all replicas** action restarts the environment selected in the breadcrumb. The event history records the request as `resource.restarted`.

## Secrets

`loco secret` manages the secrets of one environment; `--env` or `LOCO_ENV` selects it. Any service in the environment reads a secret through a variable that refers to it as `${{ secrets.NAME }}`; see [Variables and secrets](../deployment/loco-yaml.md#variables-and-secrets). `push` sends every `KEY=VALUE` line of a `.env` file (or stdin) in one call, `set` takes assignments on the command line, `unset` removes names and `list` shows names, versions, the services that read each one and who set it. No command prints a value, and `$NAME` in a `.env` file stays literal. Saving secrets leaves running replicas unchanged, and new values apply on the next deploy or restart. `set` and `push` name the services whose latest deployment reads a changed value and print the `loco resource restart` command for each. `unset` refuses a name that the latest deployment of a service reads, and names those services; remove the reference and deploy them first. Setting, pushing and removing secrets needs write access to the workspace, and deploying a service whose variables read a secret needs read access to the workspace.

```sh
loco secret push --env production .env
loco secret set --env production API_KEY=abc123
loco secret unset --env production API_KEY
loco secret list --env production
```

## Container requirements

Applications run under Kubernetes' `restricted` Pod Security profile in a user namespace (`hostUsers: false`). The image's declared user can be a name, a numeric ID or root; an image without a `USER` runs as root inside the pod. Kubernetes maps each pod's users to a separate unprivileged range on the node. Privilege escalation remains disabled, all capabilities are dropped and seccomp uses the runtime default. The pod sets the `net.ipv4.ip_unprivileged_port_start=0` sysctl, so the process can listen on any port from 1 to 65535 without root or extra capabilities.

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
| `OTEL_SERVICE_NAME` | the resource's slug, such as `checkout`; changing the slug restarts every replica |
| `OTEL_RESOURCE_ATTRIBUTES` | `deployment.environment.name` and `loco.deployment.id` |

A variable with the same name replaces that one default; Loco still injects the other three. Other OpenTelemetry variables, such as signal-specific exporter settings like `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE`, `OTEL_LOG_LEVEL` and `OTEL_TRACES_SAMPLER`, leave all four defaults in place.

Setting `injectOtel` to `false` removes the injected set on rollout and preserves user-supplied variables. It does not disable an SDK configured by the application or its image, or stop Loco from collecting telemetry sent to it. Loco does not inspect configuration inside the application or its image.

For data sent to Loco, the collector attaches workspace, environment and resource IDs from pod labels and drops application-supplied values for those IDs.

The collector preserves application-provided service identity. It does not infer `service.name`, `service.namespace`, `service.version` or `service.instance.id` from Kubernetes labels, workload names, namespaces or container images. Kubernetes identity remains available under the `k8s.*` attributes.

Each deployment records the variables it runs with, with their references to secrets unresolved. A redeploy or rollback runs the variables of the deployment it copies, and a canceled deployment's regions go back to the variables of the deployment that served them before. In `CreateDeployment`, the spec's environment map is the full set of variables; an empty map runs none.

The **Variables** section of a resource's settings lists the variables the active deployment runs with, as written. For a service deployed from the dashboard, it adds, edits and removes them, and saving redeploys every region of the environment with the new environment map. A variable reads a secret of the environment through `${{ secrets.NAME }}`. When regions of the environment run different variables, the section says so, and saving applies the list it shows to every region. For a service deployed from `loco.yaml`, the variables come from the file's `env` map and are read-only.

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

Container stdout and stderr records keep their original message. When a record has no severity, the collector extracts a string `level`, `severity` or `severityText` from a JSON object, a leading level such as `[WARNING]`, or a `level=warn` field. A timestamp such as `2026-10-19T12:00:00Z`, `2026/10/19 12:00:00` or `[2026-10-19 12:00:00 +0000]`, and after it an optional bracketed process ID such as `[1]`, can precede a leading level, as in nginx and gunicorn logs; terminal color codes do not affect detection. `warning` becomes `WARN`, `notice` becomes `INFO`, and a leading `panic` becomes `FATAL`; trace, debug, info, error and fatal levels are also recognized, without regard to case. Existing severity is preserved.

The collector drops empty lines and joins a multi-line entry into one record: lines that start with whitespace, Java `Caused by:` lines and the lines of a Go panic or goroutine trace continue the line before them. A Go panic therefore arrives as one `FATAL` record that holds its whole stack trace.

A stderr line that names no level is `ERROR`; one that names a level, such as `INFO`, keeps it. Other lines with malformed JSON, an unknown level or no recognized level remain unclassified and appear as INFO in the dashboard. A word such as “warning” inside an ordinary message does not set its severity. Structured OTLP logs retain the severity supplied by the application SDK. When a replica exports logs over OTLP, the logs view shows those records and hides the replica's stdout lines, which repeat them; its stderr lines, where crashes are printed, stay visible.

## Public, private and worker applications

An application with a domain is public: the gateway routes its hostname to the application. An application with a `port` and without a domain is private: it accepts connections only from inside its workspace, through its [private hostname](../deployment/loco-yaml.md#private-service-discovery). To deploy a private application, leave `domains` off the service in `loco.yaml`:

```yaml
services:
  cart:
    image: ghcr.io/acme/cart:2.1.0
    port: 7070
    regions:
      us-ca-1: { cpu: 100m, memory: 256Mi, replicas: { min: 1, max: 1 } }
```

An application without a `port` is a worker, such as a queue consumer: it runs, scales and writes logs like any other and makes outbound connections, but it has no hostname, takes no traffic and gets no readiness probe. Leave out `port` to deploy one:

```yaml
services:
  worker:
    image: ghcr.io/acme/worker:1.4.2
    regions:
      us-ca-1: { cpu: 100m, memory: 256Mi, replicas: { min: 1, max: 1 } }
```

`loco deploy` prints that a private application has no public URL and that a worker takes no traffic, and `loco resource status` shows `URL: none` for both. In the dashboard, the New service drawer on the Resources page offers the same choice with its Public switch. Its Port field starts empty, which creates a worker. A domain added to an existing application takes effect on its next deployment, and removing an application's last domain makes it private from its next deployment.

## Investigate a rollout

Use application status to find whether a deployment is progressing or ready. `loco resource status` shows a status for each environment the application runs in, and for each region its ready and desired replicas, its deployment and the reason a region is not ready. The ready count covers the ready replicas of every version and stops at the desired count, so a region with one desired replica reads `1/1`, not `2/1`, while a rollout overlaps old and new replicas. The region status also names its serving deployment: the one with the most ready replicas, the live deployment and then the newest on a tie.

- Healthy: ready replicas of any version cover every desired replica.
- Deploying: a first rollout has no ready replica yet.
- Degraded: some replicas or regions are not ready.
- Unavailable: no replica is ready.
- Unknown: the cluster running a region has missed three heartbeats, or reports that it is unhealthy, so its last report is not trusted. The agent sends its heartbeats on the same stream as its replica reports, at the interval the API sets with `LOCO_AGENT_HEARTBEAT_INTERVAL` (30 seconds by default), so a region goes Unknown 90 seconds after that stream drops. Loco places no new deployments on such a cluster until it heartbeats again.

A replacement keeps the previous running deployment running until the replacement is fully available. The previous deployment then becomes superseded. If the replacement fails, the previous deployment stays running. Replacing a deployment that never had every replica available cancels it, even one that shows `running`. A restart returns a deployment to deploying until its new revision is available.

Below each environment, `loco resource status` lists every replica of each region: its state, restarts, last exit (such as `OOMKilled (137) 5m ago`), age and node. A replica that restarted and is not serving reads `Restarting (2/5)`: the restarts its [failure policy](../deployment/loco-yaml.md#failure-policy) counts against the `on-failure` budget, or `Restarting (2)` under `always`. While a region reads Unknown, the list keeps the cluster's last report and ends with when it was sent. `--output json` includes the same replicas, with each replica's start time and last exit reason, exit code and finish time.

The controller reports each replica's state and the rollout's current problem, availability and deploy deadline. Replica states include scheduling, starting, serving, failing and shutdown outcomes. A problem carries one reason, a fixed message and specific diagnostic detail. Region messages and runtime notices use that problem. Reconciliation continues after a failure, so the application can recover when replicas become ready.

The status comes from the replicas the cluster reports ready, not from the deployment record. A rollout stops no previous replica before a new one has stayed ready for the worker cluster's `controller.minReadySeconds` (10 seconds by default), so a redeploy whose new replicas fail leaves the application Healthy while every previous replica still serves, and the region shows the failure. Public routes and private port 80 continue reaching each ready replica on its own application port during port changes. The failed deployment remains the region's current deployment, and the region status names the previous one as its serving deployment. See [Failure policy](../deployment/loco-yaml.md#failure-policy). Read Loco events for runtime problems, then inspect logs for application startup failures. The dashboard provides another view of the same applications.

The dashboard shows **Desired deployment** separately from **Serving deployments**. The serving list counts the ready, non-terminating replicas in each cluster's latest accepted report and identifies each replica's own deployment. An older deployment can keep serving while a new build is pending, after a failed rollout, or while a retiring cluster finishes deleting its workloads. Several deployments can serve during a rollout. Until a cluster reports the new revision, the dashboard keeps its last reported serving identities. Suspension clears those identities until a fresh report arrives after resumption. A replacement placement starts without a serving report. Historical deployment details keep their historical replica snapshots. Deployment-history and build-image retention preserve the last reported deployment context and versions with live replicas, including replicas that may become ready later. Those versions become eligible for cleanup after a newer report replaces them or workload deletion is acknowledged.

During a successful rollout, Kubernetes delays signaling a retiring service container by the worker chart's `controller.drainSeconds` (5 seconds by default). This native preStop sleep lets routing withdraw the endpoint while the process still accepts traffic. The process then retains Kubernetes' normal 30-second shutdown budget. Portless workers have no added delay. Increase the cluster's drain window when routing needs longer; a stalled control plane or routing update can outlast it. The hook applies to replacement Pods, so changing the controller setting rolls serving Pod templates and cannot protect old Pods retroactively.

`loco resource events <name>` lists Loco events, newest first. Use `--limit 20` to limit the output, `--query` to keep the events that match an [audit query](../reference/audit-queries.md), such as `--query '@type:resource.*'`, or `--output json` for event IDs, creation times and structured data. The event history records transitions: `resource.degraded` when a runtime problem changes, and `resource.recovered` when it clears. Repeated reports of the same problem do not add events. A replica that restarted keeps its `restarting` or `out_of_memory` problem until it has gone the worker cluster's `controller.healthyAfterSeconds` (5 minutes by default) without another restart. A replica that restarts more often than its service's [failure policy](../deployment/loco-yaml.md#failure-policy) allows, 2 times without staying up for the health window by default, reports `restarts_exhausted`. Until its version proves itself, that stops the version; once the version has served on every replica for the health window without a restart, the replica keeps running and the deployment is `crashed` until the replica stays up for the health window again. A new version that is not healthy within its deploy timeout, 10 minutes by default, stops with `deploy_timeout`. The previous version takes its place, and the deployment fails, when its replicas are still running or when it proved itself by serving on every replica for the health window without a restart. A deploy timeout with nothing to fall back to also fails the deployment. A restart stop with nothing to fall back to, including a version restarted after it stopped, leaves the service with no replicas, and its deployment becomes `crashed` until `loco resource restart`, a redeploy or a rollback starts it again with a fresh budget. A running deployment whose replica restarted or stopped being ready is `degraded` until the controller reports it healthy again.

Each restart adds an activity entry that names its cause and, under `on-failure`, the budget, such as `Restarted after exit 1 · 1 of 2`, `Restarted after failing its health check · 2 of 2` or `Restarted after running out of memory · 1 of 2`. The kubelet records the liveness kill as an event that can arrive after the restart, so an entry written before it arrives names the exit instead.

Runtime events use the deployment's problem reason, such as `restarting`, `restarts_exhausted`, `deploy_timeout`, `out_of_memory`, `image_pull_failed`, `unschedulable` or `health_check_failing`. Events include a Loco-written message, placement ID and region in their data, with organization, workspace, resource and environment IDs for filtering. The dashboard's events drawer and warning count use this history; its restarts card shows recent logs for the affected resource, environment and region. Kubernetes reasons and replica names are not included in events.

`EventService.ListEvents` reads one organization, workspace or resource, optionally one environment and a creation time range, and its `query` field selects events by actor, target, event type and request ID with an [audit query](../reference/audit-queries.md). The dashboard's audit log search box sends its text as that query, so it searches the whole log.

```sh
loco web
```

To define services and their domains, see [loco.yaml](../deployment/loco-yaml.md).
