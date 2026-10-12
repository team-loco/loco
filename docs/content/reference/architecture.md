# Architecture

Loco separates its control plane from the regional components that run applications. The CLI and dashboard call the API, and the regional agent connects the cluster to the control plane.

## Control plane

The Go API exposes ConnectRPC services and stores platform state in PostgreSQL. The CLI uses Cobra and Charm libraries; the dashboard lives in `web/`. Protocol definitions live in `proto/`, with generated clients in `gen/`.

## Regional runtime

The in-cluster agent exchanges state with the API. The controller reconciles Application custom resources into Kubernetes workloads and routing resources. Cilium provides networking; Envoy Gateway handles ingress and TLS termination; cert-manager provisions certificates.

## Observability

OpenTelemetry collects telemetry and ClickHouse stores observability data. The regional observability proxy provides access to the regional data.

The observability proxy owns the ClickHouse schema. Before it serves, it applies the versioned migrations in `observability-proxy/migrations` to the `loco_obs` database, and `/readyz` fails until they succeed. Replicas take turns: each one holds the Kubernetes Lease `obsProxy.clickhouse.migrations.lease.name` in the proxy's namespace while it migrates, renews it until it finishes and releases it after, so one migration runs at a time and a replica that finds the schema current applies nothing. A replica that dies holding the Lease blocks the others until the Lease expires. `obsProxy.clickhouse.migrations.retryBudgetSeconds` covers waiting for the Lease and migrating together; a replica that runs out of it exits and restarts. The collectors only insert. Every table has `OrganizationId`, `WorkspaceId`, `EnvironmentId` and `ResourceId` columns, computed from the `loco.organization.id`, `loco.workspace.id`, `loco.environment.id` and `loco.resource.id` resource attributes, and its ordering key starts with `WorkspaceId`. The collectors set those attributes from the `loco.io/*-id` labels the controller puts on app and builder pods, and drop any value a client sends; Envoy's own telemetry takes `WorkspaceId` and `ResourceId` from the route instead and has no `OrganizationId` or `EnvironmentId`. `QueryMetrics` filters on `WorkspaceId` and the requested `ResourceId`s, and on `EnvironmentId` when the request sets `environment_id`, so a query that names an environment leaves out Envoy's metrics. The dashboard's resource page names the active environment and sends the query to the proxy of the cluster that runs the region's current deployment. The deployment collector watches Kubernetes events and writes them to their own table, `otel_k8s_events`, so log queries never return them. An event about a pod takes the pod's tenancy when the pod has the event's namespace and UID; an event about a ReplicaSet or Deployment takes the tenancy of a pod that belongs to it in that namespace. Events about anything else, or about an object no Loco pod belongs to, have no tenancy and no query returns them. `QueryEvents` filters on `WorkspaceId`, the requested `ResourceId`s and `EnvironmentId` before it collapses each event's updates into its latest state and applies the limit. Rows expire after the `obsProxy.clickhouse.retention` durations for logs, traces and metrics; a migration applies them when it creates a table, so changing a duration later does not alter an existing table.

## Deployment boundaries

[Deployment modes](../deployment/modes.md) determine the operator and tenancy of the platform. Application workspaces, environments, and regions determine where a team's workloads run within the installation.

For the protobuf service definitions and client generation, see [CLI and API reference](cli-api.md).
