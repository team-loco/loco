---
title: "Self-hosting"
---

Self-hosting runs Loco's control plane and worker software on operator-managed Kubernetes clusters through Flux. Operators own infrastructure, credentials, release pins and backups.

## Components

| Component | Responsibility |
| --- | --- |
| API and dashboard | Platform state, authentication and application management |
| PostgreSQL | Platform database, operator-supplied or a bundled CloudNativePG cluster |
| OpenBao | Application encryption and telemetry signing; managed or external |
| ClickHouse and observability proxy | Telemetry storage and ingest; managed or external |
| Agent and controllers | Apply applications and builds on workers |
| Gateway and cert-manager | Routing and certificates |
| Flux | Reconcile the versioned platform bundle and per-cluster configuration |

The control-plane chart is `charts/loco-control-plane`; the worker chart is `charts/loco-worker`. The platform bundle pins charts, CRDs and images by digest. Use its `./control-plane`, `./worker` or `./all` entry point, and add `./postgres-operator` for the bundled PostgreSQL. Customer workloads continue through the API, agent and `Application` resources.

## Application worker requirements

Application workers require Kubernetes 1.35 or newer and support for [pod user namespaces](https://kubernetes.io/docs/concepts/workloads/pods/user-namespaces/). Use Linux with idmapped mount support on the kubelet and pod volume filesystems (normally Linux 6.3 or newer), a compatible OCI runtime such as runc 1.2 or newer, and containerd 2.0 or newer or CRI-O 1.25 or newer.

Every application pod requests `hostUsers: false`. A worker without support fails to start the pod; Loco never falls back to running it in the host user namespace. User namespaces share the node kernel and do not provide a virtual machine boundary between tenants.

## Installation inputs

Provide Kubernetes and its CNI, PostgreSQL, OIDC, an S3-compatible source bucket, a registry and DNS. The bundle can run PostgreSQL instead: `locoadm init --postgres` installs the CloudNativePG operator and sets the control-plane value `postgres.enabled`, which runs a two-instance cluster for the API on the default StorageClass. It has no backups yet; see [bundled PostgreSQL](../reference/installation.md#bundled-postgresql). `locoadm init --registry HOST` runs a zot registry in the control plane at that hostname, in Loco's token mode, on a volume or an S3-compatible bucket; see [bundled registry](../reference/installation.md#bundled-registry). Platform staging and production use separate clusters. User environments, such as a team’s staging and production, can share the installation’s worker clusters. A single-cluster installation can host one control plane and one worker. Use `locoadm init --single-cluster --name local` to generate its `clusters/local/` directory and combined bundle entry point.

Follow the [installation guide](../reference/installation.md) to scaffold the operator repository, review chart values, configure [certificate solvers](../reference/installation.md#configure-certificate-solvers) for your DNS provider, encrypt Secrets and bootstrap Flux. Each cluster has its own Git directory referencing a shared role base in `bases/` and exactly one cohort file from `releases/`. Shared values merge before per-cluster overrides. `locoadm` emits plaintext Secret manifests; operators choose encryption or external Secret delivery. With SOPS, configure Flux decryption and keep an offline recipient for recovery. Credential Secrets are referenced by name. Join tokens are one-time bootstrap material: bootstrap the worker’s Flux first, then run `locoadm cluster create --country CC --subdivision SUB --kube-context WORKER` with the default one-hour TTL. It creates the worker's region from the country and state or province and prints the region code the worker chart's `global.region` holds; see [regions](../reference/regions.md). The command writes the Secret directly and prints only a confirmation. An encrypted token in Git is an option for fully unattended installation. Chart values expose API limits, service defaults, retention and join-token lifetimes; `values.schema.json` validates their types and supported forms. The API validates configuration at startup. The worker chart's `global.domain.apps` lists the app domains services are named under; the worker's Gateway serves `*.<app domain>` for each. After the first `locoadm cluster create`, set the control-plane value `defaultRegion` to the region code it prints; see [regions](../reference/regions.md#default-region). Set `api.env.LOCO_TARGET_PLATFORM` to `linux/arm64` for ARM applications, or retain the `linux/amd64` default. The target applies across deployment regions. Each destination must report matching worker nodes, and source builds run on native nodes for that target. Existing build and deployment snapshots retain their target; rebuild source images before deploying them under a changed target.

Source builds pull the base images of a Dockerfile from their registries. To pull them through a registry mirror, such as a pull-through cache for Docker Hub, set the `loco-worker` value `builds.buildkitdConfig` to a [BuildKit `buildkitd.toml`](https://github.com/moby/buildkit/blob/master/docs/buildkitd.toml.md) and add the mirror's address and port to `builds.privateEgress` when it is on a private network:

```yaml
builds:
  buildkitdConfig: |
    [registry."docker.io"]
      mirrors = ["registry-cache.internal:5000"]
  privateEgress:
    - cidr: 10.0.12.7/32
      port: 5000
```

The control-plane chart generates missing ClickHouse credentials and preserves existing ones. OpenBao supports static, KMS or transit auto-unseal. Keep the seal material, recovery shares and snapshots under separate backup and custody procedures. External ClickHouse and OpenBao remain supported through their chart settings.

The worker chart enables branded application error pages when `env.CONTROL_PLANE_URL` uses HTTPS and the API certificate is trusted by Envoy's system roots. Upgrade the control plane before enabling the worker fallback.

## Installation roles

An installation role applies to the whole installation rather than one organization. There are two:

| Role | Grants |
| --- | --- |
| System read | Read every organization, workspace and resource as a `read` member would, and call the read-only installation RPCs: list organizations, clusters, regions, users and installation roles, and stream every event. It allows no change; a change the user's own organization, workspace or resource grants do not allow answers `PermissionDenied`. |
| System admin | Everything system read grants, plus change access to every organization, workspace and resource (rename an organization, create workspaces, delete resources) and the installation's administration: register and manage clusters, grant and revoke installation roles, suspend organizations and users, set organization limits and rewrap secret keys. |

Set verified email addresses in `api.systemAdmins` and `api.systemReaders`. Their first sign-in claims the listed role, including under closed signup. An email in both lists fails the API's startup:

```sh
locoadm login --api https://api.example.com --web https://app.example.com
locoadm admin list
```

Configure the dashboard issuer and callback in the control-plane values and referenced Secret. `locoadm login --device` supports browser approval on another machine. [Dashboard identity provider setup](../reference/installation.md#register-the-dashboard-with-your-identity-provider)

For a separately hosted dashboard, forward `/api` on the dashboard origin to the control-plane API and strip the prefix. If the browser connects directly to an API on a sibling host, serve both hosts over HTTPS, allow the dashboard origin for API CORS and add the API origin to the dashboard's `connect-src` policy. Dashboard and API hosts on different sites cannot use the session cookies.

A system admin grants a role with `locoadm admin grant USER_UUID --role read|admin` (`admin` by default), changes it by granting the other role, and removes it with `revoke USER_UUID`. A user holds the higher of their configured and granted roles. A configured role changes only through the configuration: revoking it, or granting less than it, fails with `FailedPrecondition`. A grant or revoke that would leave the installation without a system admin fails with `FailedPrecondition`. `locoadm admin list` shows each user's name, last active time, configured and granted role and the configured emails nobody has claimed (the last active time is the last recorded session activity, `—` for a user with no session); `locoadm status` lists the unclaimed admin emails. [Cluster enrollment](../reference/cluster-attachment.md) describes worker registration and join-token Secret delivery.

`OrgService.ListOrgs` lists every organization in the installation, whether or not the caller belongs to it, and answers `PermissionDenied` to anyone without system read. Each entry carries the organization's name, ID, suspension and reason, owners (its admins, by name and email), member, workspace and resource counts and creation time. `query` matches names, IDs and owner emails ignoring case, `state` keeps active or suspended organizations, `order_by` sorts by name or creation time (newest first by default), and `include_total_count` sets `total_count`, exact up to 10,000. `locoadm org list [--query TEXT] [--state active|suspended]` prints the same as a table.

### Installation audit log

`EventService.ListEvents` without an `org_id` lists the events of every organization and the installation's own, newest first, with the same `query`, paging and total count as an organization's audit log. It needs system read. `org_id` narrows it to one organization, and `installation_only` keeps the events that belong to no organization. `locoadm audit` calls it and prints a table; `--org ORG_UUID`, `--installation-only`, `--query` and `--limit` (default 100) select events.

Cluster registration, join-token issue, pause, resume, removal and restore, user suspension and reinstatement, and installation role claims, grants and revokes record events with no organization. Organization members never see them.

An event in an organization carries `acted_as_operator` when the user who made the change holds no role in that organization when the event is recorded: no organization, workspace or resource scope in it. The organization's own audit log lists it, and `locoadm audit` labels the actor "Loco admin". A member of the organization is never marked, even when they also hold an installation role. An API token follows the user behind it.

## Health endpoints

The API serves these unauthenticated endpoints.

| Path | Answers | Use |
| --- | --- | --- |
| `/livez` | `200` while the process serves HTTP. It checks no dependency. | Kubernetes liveness and startup probes |
| `/health` | The same as `/livez`. | Probes of the latest release's chart during an upgrade |
| `/readyz` | `200` when PostgreSQL and OpenBao answer, otherwise `503` with a body such as `postgres unavailable` or `transit unavailable`. The checks share one 2-second timeout, and the body never includes connection details. | Kubernetes readiness probe and an external uptime monitor |

The chart's probes use these paths. A database outage removes the API pods from the Service without restarting them. When every API pod is unready, the gateway answers `503` itself, without a dependency name. An uptime monitor probes `https://<controlPlaneHostname>/readyz`, or `/api/readyz` when the dashboard and API share a hostname.

## Releases and operations

Upgrade the control plane first, staging workers second and production workers last by changing cohort pins in `releases/`. Staging cohorts can follow stable semver releases; production cohorts pin reviewed digests. The API supports agents at its own minor version and one minor behind. `locoadm status` reports unsupported or ahead versions and heartbeats from the API. Read the gateway’s load balancer address with `kubectl get gateway -n loco-system` on the worker or through its provider. Point the application wildcard at it once, or run external-dns.

Flux installs CRDs before dependent releases and never prunes them. The API Deployment uses `Recreate`: Kubernetes stops its previous replicas before replacements apply startup migrations. API upgrades briefly interrupt dashboard and CLI requests; deployed workloads continue running. When running the API outside this chart, stop every previous API process before starting a version that removes database fields.

Databases retain applied migrations. Reverting a cohort pin does not undo schema changes; before rolling back across a destructive migration, restore a compatible database backup and coordinate the software version with it. The control-plane HelmRelease retries failed upgrades every five minutes without automatically rolling back its software. Correct the failure and roll forward, or restore software and database together. Other releases keep their configured rollback policy.

Use `locoadm verify` against a configured worker and authorized environment to check source builds, encrypted secret delivery, HTTPS and logs. Export recovery material with explicit Kubernetes context and namespace flags. Verification and export do not prove backups can restore; run a separate restoration exercise and retain external-service backups. The bundled PostgreSQL takes no backups; back it up yourself, for example with `pg_dump`.
