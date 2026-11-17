---
title: "Self-hosting"
---

Self-hosting runs Loco's control plane and worker software on operator-managed Kubernetes clusters through Flux. Operators own infrastructure, credentials, release pins and backups.

## Components

| Component | Responsibility |
| --- | --- |
| API and dashboard | Platform state, authentication and application management |
| PostgreSQL | Operator-supplied platform database |
| OpenBao | Application encryption and telemetry signing; managed or external |
| ClickHouse and observability proxy | Telemetry storage and ingest; managed or external |
| Agent and controllers | Apply applications and builds on workers |
| Gateway and cert-manager | Routing and certificates |
| Flux | Reconcile the versioned platform bundle and per-cluster configuration |

The control-plane chart is `charts/loco-control-plane`; the worker chart is `charts/loco-worker`. The platform bundle pins charts, CRDs and images by digest. Use its `./control-plane`, `./worker` or `./all` entry point. Customer workloads continue through the API, agent and `Application` resources.

## Installation inputs

Provide Kubernetes and its CNI, PostgreSQL, OIDC, an S3-compatible source bucket, a registry and DNS. Postgres is outside the bundle. Each worker environment uses its own clusters. A single-cluster installation can host one control plane and one worker.

Follow the [installation guide](../reference/installation.md) to scaffold the operator repository, review chart values, encrypt Secrets and bootstrap Flux. Each cluster has its own Git directory referencing a shared role base in `bases/`. Shared values merge before per-cluster overrides. `locoadm` emits plaintext Secret manifests; operators choose encryption or external Secret delivery. With SOPS, configure Flux decryption and keep an offline recipient for recovery. Credential Secrets are referenced by name. Join tokens are one-time bootstrap material: bootstrap the worker’s Flux first, then run `locoadm cluster create --kube-context WORKER` with the default one-hour TTL. The command writes the Secret directly and prints only a confirmation. An encrypted token in Git is an option for fully unattended installation. Chart values expose API limits, service defaults, retention and join-token lifetimes; `values.schema.json` validates their types and supported forms. The API validates configuration at startup.

The control-plane chart generates missing ClickHouse credentials and preserves existing ones. OpenBao supports static, KMS or transit auto-unseal. Keep the seal material, recovery shares and snapshots under separate backup and custody procedures. External ClickHouse and OpenBao remain supported through their chart settings.

## First system admin

Set verified email addresses in `api.systemAdmins`. Their first sign-in claims system-admin access, including under closed signup:

```sh
locoadm login --api https://api.example.com --web https://app.example.com
locoadm admin list
```

Only use an identity provider that verifies email addresses. Register a public authorization-code client with PKCE and allow the dashboard's `/auth/callback`. `locoadm login --device` supports browser approval on another machine. Configure OIDC, API endpoints, source storage and registry settings in the control-plane values and referenced Secret.

Manage administrator grants with `locoadm admin grant USER_UUID` and `revoke USER_UUID`. Status lists configured emails that remain unclaimed. [Cluster enrollment](../reference/cluster-attachment.md) describes worker registration and join-token Secret delivery.

## Releases and operations

Upgrade the control plane first, staging workers second and production workers last by changing cohort pins in `releases/`. Staging cohorts can follow stable semver releases; production cohorts pin reviewed digests. The API supports agents at its own minor version and one minor behind. `locoadm status` reports unsupported or ahead versions, heartbeats and DNS targets from the API.

Flux installs CRDs before dependent releases and never prunes them. Database migrations retain compatibility through a rollout. Roll back software by reverting a cohort pin; databases retain their applied migrations. Failed chart upgrades use the bundle's retry and rollback policy.

Use `locoadm verify` against a configured worker and authorized environment to check source builds, encrypted secret delivery, HTTPS and logs. Export recovery material with explicit Kubernetes context and namespace flags. Verification and export do not prove backups can restore; run a separate restoration exercise and retain external-service backups.
