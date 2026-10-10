# Self-hosting

A self-hosted Loco installation puts platform operation under your team's control. The repository contains the platform components and Helm charts; production operation requires instance-specific configuration.

Start by running the [local development environment](../contributing/development.md) to exercise the components together:

```sh
mise run setup
mise run tilt
```

The local environment is a development installation. Review the repository's environment configuration before adapting it to a production cluster.

## Platform components

| Component | Repository location | Responsibility |
| --- | --- | --- |
| API | `api/` | ConnectRPC services and PostgreSQL state |
| Dashboard | `web/` | Browser access to the API |
| Regional agent | `agent/` | Connect the region to the control plane |
| Application controller | `controller/` | Reconcile Application custom resources |
| Custom resource definitions | `k8sapi/` | Kubernetes API types |
| Platform charts | `charts/` | Kubernetes platform configuration |
| Regional observability proxy | `observability-proxy/` | Access to regional observability |

## Installation inputs

Configure the API's database, cache, authentication, registry, and API/dashboard endpoints using the repository's [environment template](https://github.com/team-loco/loco/blob/main/.env.example). Check the template and deployed release together; authentication and registry changes are under active review. Choose a [secrets key provider](secrets-key-provider.md) for the key that protects environment secrets.

The cluster needs Cilium, Envoy Gateway, cert-manager, and the Loco controller. Observability uses OpenTelemetry, ClickHouse, and Grafana. The charts and environment values define their configuration.

Your team supplies DNS records, certificate issuance, persistent storage, backups, and capacity. Keep credentials separate across installations and environments. Use [architecture](../reference/architecture.md) to identify the control-plane and regional boundaries.

## Upgrades

Every commit to `main` publishes the agent, builder, controller, API, observability proxy and UI images tagged `sha-<commit>`, and each binary reports that tag as its version. The charts have no default tag for these images: set `agent.image.tag`, `controller.image.tag`, `builds.builderImage.tag` and `obsProxy.image.tag` to the same `sha-<commit>`. Regenerate CRDs through `mise run controller:gen` when developing schema changes. Review database migrations and infrastructure changes before upgrading a running installation.

A supported production packaging and upgrade procedure is not established by these docs. Inspect the chart values and the release's configuration before deployment.
