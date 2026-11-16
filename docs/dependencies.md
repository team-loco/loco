# Component dependencies

Loco's dependencies connect the control plane, worker clusters and clients through the contracts below. See the maintained [architecture reference](content/reference/architecture.md) for runtime behavior and [self-hosting guide](content/operations/self-hosting.md) for installation requirements.

## Builds and images

The CLI packs source and uploads it to S3-compatible storage through a URL the API signs. The API records the Build and dispatches it to an enrolled agent. The agent creates a Build custom resource; the build controller runs a Kubernetes Job with the builder's fetch and push steps around BuildKit. The builder downloads source, restores the registry cache, and pushes the resulting image and cache. The API records the image digest for deployment.

The registry implements the OCI image distribution interface. Operators configure static credentials or Loco's token authentication; the agent distributes credentials to the worker and the controllers attach them to builds and workloads. Registry hostnames, credentials and token settings belong to the installation. See [registry access](content/reference/architecture.md#registry-access) and the [worker chart's credential contract](../charts/loco-worker/README.md#registry-credentials).

## Platform state and authentication

The API owns platform state in PostgreSQL and exposes ConnectRPC services to the CLI, dashboard and agents. Protobuf definitions in [`proto/`](../proto/) define the RPC contract; generated clients live in [`gen/`](../gen/). Agents apply desired Application and Build resources to their clusters and report status to the API. Controllers reconcile those resources through the Kubernetes API.

User authentication accepts configured OIDC issuers through `AUTH_ISSUERS`, including issuer, audience, claims and browser client settings. The CLI and dashboard authenticate with that configuration. Cluster enrollment and agent assertions authenticate worker communication. OpenBao Transit supplies the keys for platform secret encryption and signed registry and telemetry tokens. See [API configuration](../charts/loco-control-plane/README.md) and [secrets key provider](content/operations/secrets-key-provider.md).

## Networking and certificates

Worker clusters require a CNI that enforces NetworkPolicy. The Application controller creates workloads, Services, HTTPRoutes and workspace network policies. Envoy Gateway implements Gateway API routing and TLS termination; cert-manager issues certificates using the installation's configured issuer. CRDs are installed separately from the Loco charts. The [self-hosting guide](content/operations/self-hosting.md) owns the supported Kubernetes range and upgrade rules.

## Telemetry and dashboard

Worker OpenTelemetry collectors send logs, metrics and traces over OTLP/HTTP to the configured exporter endpoint. Loco's observability proxy authenticates ingest, migrates the ClickHouse schema and writes telemetry. The API reads ClickHouse and serves queries and live log tails. See [observability](content/reference/architecture.md#observability) for ownership, tenancy and authentication.

The React dashboard calls the API through generated ConnectRPC clients. The UI image serves its static build and documentation with Static Web Server, configured in [`web/sws.toml`](../web/sws.toml).

## Version sources

Read versions from the manifests and lockfiles that builds and dependency updates use:

| Dependency | Canonical files |
| --- | --- |
| Go libraries | Each component's `go.mod` and `go.sum`; shared libraries in the root module |
| Dashboard, docs and client generation | Workspace `package.json` files and root [`bun.lock`](../bun.lock) |
| Development and CI tools | [`mise.toml`](../mise.toml) and [`mise.lock`](../mise.lock) |
| Helm dependencies | [`loco-control-plane/Chart.yaml`](../charts/loco-control-plane/Chart.yaml), [`loco-worker/Chart.yaml`](../charts/loco-worker/Chart.yaml) and their `Chart.lock` files |
| cert-manager | [`charts/cert-manager.yaml`](../charts/cert-manager.yaml) |
| Runtime images | Component Dockerfiles and chart `values.yaml` files |
| Platform release artifacts | [`tools/platform-release/crds.yaml`](../tools/platform-release/crds.yaml) and the [release contract](../tools/platform-release/README.md) |

Hosted installation credentials, provider accounts and deployment configuration belong outside this repository.
