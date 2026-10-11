# Application operations

The CLI reads application status and manages application resources through the selected Loco API. Check command help for the arguments supported by your release:

```sh
loco resource --help
loco resource status --help
loco resource logs --help
loco resource events --help
```

`loco resource` also includes `env`, `scale`, and `destroy`. Inspect an operation's help before changing resources or removing an application.

## Container requirements

Applications run under Kubernetes' `restricted` Pod Security profile. Images must run as a numeric non-root user, such as `USER 10001`, and use an unprivileged application port. Configure the routing port to match the port the application listens on.

## Runtime environment

The controller sets these variables in every application container:

| Variable | Value |
|---|---|
| `LOCO_APP_NAME`, `LOCO_RESOURCE_ID`, `LOCO_WORKSPACE_ID`, `LOCO_DEPLOYMENT_ID` | the application's identifiers |
| `LOCO_REGION`, `LOCO_ENVIRONMENT` | the region and environment the deployment targets |
| `LOCO_INTERNAL_DOMAIN`, `LOCO_PUBLIC_DOMAIN` | the in-cluster hostname, and the public hostname or an empty string |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL` | the region's OpenTelemetry collector over OTLP/HTTP (`http/protobuf`) |
| `OTEL_SERVICE_NAME` | the application's workload name, `resource-<resource id>` |
| `OTEL_RESOURCE_ATTRIBUTES` | `deployment.environment.name` and `loco.deployment.id` |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | `parentbased_traceidratio` at `[Obs.Tracing] SampleRate` when tracing is enabled, otherwise `parentbased_always_off` |

An OpenTelemetry SDK reads the `OTEL_*` variables, so an instrumented application exports to Loco without configuration. With tracing disabled, the application starts no traces of its own but records spans for requests the gateway sampled at ingress, so a trace runs from the gateway into the application. The collector attaches the workspace, environment and resource IDs from the pod's labels and drops any values the application sends for them.

An environment variable you set with `loco resource env` or `[Env]` replaces the `OTEL_*` variable of the same name. `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_EXPORTER_OTLP_PROTOCOL` go together: setting either one drops both of Loco's values, so an endpoint is never paired with the wrong protocol. The `LOCO_*` variables cannot be overridden.

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

Use application status to find whether a deployment is progressing or ready. Read events for scheduling and reconciliation failures, then inspect logs for application startup failures. The dashboard provides another view of the same applications.

```sh
loco web
```

To define services and their domains, see [loco.yaml](../deployment/loco-yaml.md).
