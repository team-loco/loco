# Application operations

The CLI reads application status and manages application resources through the selected Loco API. Check command help for the arguments supported by your release:

```sh
loco resource --help
loco resource status --help
loco resource logs --help
loco resource events --help
```

`loco resource` also includes `env`, `scale`, and `destroy`. Inspect an operation's help before changing resources or removing an application.

## Scale

`loco resource scale` changes a service's replicas, CPU or memory in one environment and rolls out a new deployment in each region the service runs in there. `--env` or `LOCO_ENV` selects the environment; a workspace with a single environment uses it, and a workspace with several fails without one. Other environments keep their deployments. The dashboard's Settings tab scales the environment selected in the breadcrumb.

```sh
loco resource scale web --env production --replicas 3
loco resource scale web --env staging --cpu 250m --memory 512Mi
```

## Secrets

`loco env` manages the secrets of one service in one environment. `--service` names the service and is required; `--env` or `LOCO_ENV` selects the environment. Secrets belong to that service only, so two services that need the same value each get it set. `push` sends every `KEY=VALUE` line of a `.env` file (or stdin) in one call, `set` takes assignments on the command line, `unset` removes names and `list` shows names, versions and who set them. No command prints a value, and `$NAME` in a `.env` file stays literal. `unset` refuses a name that the service's active deployment declares in `loco.yaml`; remove it there and apply first.

```sh
loco env push --service web --env production .env
loco env set --service web --env production API_KEY=abc123
loco env unset --service web --env production API_KEY
loco env list --service web --env production
```

## Container requirements

Applications run under Kubernetes' `restricted` Pod Security profile. Use an image with a numeric non-root `USER`, or set `runAsUser` in `loco.yaml` to a positive numeric UID compatible with the image. Set `runAsGroup` when it also needs an explicit primary GID; see [Runtime user](../deployment/loco-yaml.md#runtime-user). Use an unprivileged application port and configure the routing port to match it.

## Runtime environment

By default, the controller sets these variables in every application container:

| Variable | Value |
|---|---|
| `LOCO_APP_NAME`, `LOCO_RESOURCE_ID`, `LOCO_WORKSPACE_ID`, `LOCO_DEPLOYMENT_ID` | the application's identifiers |
| `LOCO_REGION`, `LOCO_ENVIRONMENT` | the region and environment the deployment targets |
| `LOCO_INTERNAL_DOMAIN`, `LOCO_PUBLIC_DOMAIN` | the in-cluster hostname, and the public hostname or an empty string |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL` | the region's OpenTelemetry collector over OTLP/HTTP (`http/protobuf`) |
| `OTEL_SERVICE_NAME` | the application's workload name, `resource-<resource id>` |
| `OTEL_RESOURCE_ATTRIBUTES` | `deployment.environment.name` and `loco.deployment.id` |

An instrumented application whose OpenTelemetry SDK supports environment configuration uses these variables to export to Loco. The collector attaches the workspace, environment and resource IDs from the pod's labels and drops any values the application sends for them.

An `OTEL_*` variable in `loco.yaml` under `env`, or named in `secrets` and set with `loco env`, replaces Loco's connection or identity default when the name matches. Sampler variables pass directly to the SDK. Plain values take precedence when the same name also appears in `secrets`. `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_EXPORTER_OTLP_PROTOCOL` go together: setting either one drops both of Loco's values, so an endpoint is never paired with the wrong protocol. The `LOCO_*` variables cannot be overridden.

Dashboard redeploy and rollback keep the current plain environment values and declared secret names for the target environment and region. `CreateDeployment` has the same behavior when its environment map is empty; a nonempty map replaces the plain values while retaining the secret names. To remove plain values or secret declarations, edit `loco.yaml` and apply it.

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

Container stdout and stderr records keep their original message. When a record has no severity, the collector extracts a string `level`, `severity` or `severityText` from a JSON object, a leading level such as `[WARNING]`, or a `level=warn` field. An ISO timestamp can precede a leading level; terminal color codes do not affect detection. `warning` becomes `WARN`; trace, debug, info, error and fatal levels are also recognized, without regard to case. Existing severity is preserved.

Malformed JSON, unknown levels and messages without a recognized level remain unclassified and appear as INFO in the dashboard. A word such as “warning” inside an ordinary message does not set its severity. Structured OTLP logs retain the severity supplied by the application SDK.

## Public and private applications

An application with a domain is public: the gateway routes its hostname to the application. An application without a domain is private: it runs, scales and writes logs like any other, makes outbound connections, and accepts connections only from inside its workspace. To deploy a private application, leave `domains` off the service in `loco.yaml`:

```yaml
services:
  worker:
    image: ghcr.io/acme/worker:1.4.2
    regions:
      us-east-1: { cpu: 100m, memory: 256Mi, replicas: { min: 1, max: 1 } }
```

`loco deploy` then prints that the application has no public URL, and `loco resource status` shows `URL: none`. The dashboard's new-service form offers the same choice under Networking. A domain added to an existing application takes effect on its next deployment, and removing an application's last domain makes it private from its next deployment.

## Investigate a rollout

Use application status to find whether a deployment is progressing or ready. `loco resource status` shows a status for each environment the application runs in, and for each region its ready and desired replicas, its deployment and the reason a region is not ready:

- Healthy: every desired replica is ready.
- Deploying: a first rollout has no ready replica yet.
- Degraded: some replicas or regions are not ready.
- Unavailable: no replica is ready.
- Unknown: the cluster running a region has missed three heartbeats, or reports that it is unhealthy, so its last report is not trusted. The agent sends its heartbeats on the same stream as its replica reports, at the interval the API sets with `LOCO_AGENT_HEARTBEAT_INTERVAL` (30 seconds by default), so a region goes Unknown 90 seconds after that stream drops. Loco places no new deployments on such a cluster until it heartbeats again.

The controller reports a failure when a current replica is unschedulable or Kubernetes reports that the current rollout exceeded its progress deadline. The region's failure message includes scheduling details or directs you to inspect replicas and logs. Reconciliation continues after a failure, so the application can recover when replicas become ready.

The status comes from the replicas the cluster reports ready, not from the deployment record. A redeploy whose new replicas fail leaves the application Healthy while the previous replicas still serve, and the region shows the failure. Read events for scheduling and reconciliation failures, then inspect logs for application startup failures. The dashboard provides another view of the same applications.

```sh
loco web
```

To define services and their domains, see [loco.yaml](../deployment/loco-yaml.md).
