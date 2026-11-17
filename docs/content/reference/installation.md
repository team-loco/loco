---
title: "Installation"
---

A Loco installation uses Flux to reconcile a versioned platform bundle and an operator repository containing cluster values and operator-delivered Secrets. PostgreSQL and the cluster network are supplied by the operator.

## Prepare infrastructure

Provide Kubernetes with its CNI, PostgreSQL, an OIDC provider, an S3-compatible source bucket, a registry and DNS. Allow the cluster to pull the platform charts, images and OCI artifacts. The [GitOps example](https://github.com/team-loco/loco/tree/main/examples/gitops) documents the minimum Flux version and the input Secrets.

Each cluster runs its own Flux and reads its own directory. The control plane uses `./control-plane`, a worker uses `./worker`, and a single-cluster installation uses `./all`. Keep staging and production workers on separate clusters.

Register one public OIDC client with authorization code and PKCE; allow the dashboard origin followed by `/auth/callback`. Use a provider that verifies administrator email addresses.

## Scaffold the repository

Supply a published bundle digest and an environment file containing operator-supplied API credentials:

```sh
locoadm init --output ./fleet \
  --api https://api.example.com --web https://app.example.com \
  --domain apps.example.com --region example-east --admin admin@example.com \
  --bundle oci://ghcr.io/team-loco/loco/platform@sha256:BUNDLE_DIGEST \
  --api-env-file ./api.env
```

`init` generates a static OpenBao seal key and writes it and the API environment as plaintext Secret files with mode `0600`. Encrypt these files before committing, or deliver the named Secrets through External Secrets. Core has no SOPS dependency; the generated README documents operator-managed encryption and Flux decryption. The worker token starts empty. Review the generated values and supply storage, database, OIDC, registry and DNS credentials before reconciliation. Chart defaults define shared settings; each cluster overrides them in `values/`.

The repository has `releases/control-plane.yaml`, staging and production worker cohorts, and one directory under `clusters/` per cluster. Staging can follow stable releases through a semver range. Production pins an exact bundle digest.

## Bootstrap Flux

Bootstrap Flux against the cluster's directory with your Git host's supported authentication. For example:

```sh
flux bootstrap git --url=ssh://git@example.com/operator/fleet \
  --branch=main --path=clusters/control-plane --private-key-file=./deploy.key
```

Use a different deploy key on each cluster. If you choose SOPS, install it through your own tooling, encrypt the Secret files, configure `spec.decryption` on `loco-cluster-config`, and provision the cluster key in `flux-system` before reconciliation. Keep an offline recipient for recovery. External Secrets can supply the same named Secrets without Flux decryption.

The bundle supplies namespaces and the reconciler service account. Its namespace layer runs first; the operator's `loco-cluster-config` applies Secrets and optional issuers. Namespaces and CRDs are never pruned. Infrastructure installs cert-manager and an independent gateway release. Control-plane and worker releases wait for infrastructure and cluster configuration.

Each HelmRelease accepts an optional `<release>-values` ConfigMap and `<release>-secrets` Secret in `flux-system`, with a `values.yaml` key. Standalone credential Secrets live in the workload namespace and are referenced by name. Image digests remain pinned inside the bundle. The control-plane chart's credential Job creates missing ClickHouse credentials and preserves existing ones. OpenBao initialization and auto-unseal run through chart Jobs.

## Sign in and enroll workers

```sh
locoadm login --api https://api.example.com --web https://app.example.com
locoadm status
```

Sign in with a verified email listed in `api.systemAdmins` to claim the first system administrator. `--device` supports approval from another machine. `loco` and `locoadm` share the resulting session.

Register each worker and capture its plaintext join-token Secret:

```sh
locoadm cluster create --name worker-staging --region example-east --tier staging \
  --ttl 72h -o secret > ./fleet/clusters/worker-staging/config/join-token.yaml
```

Encrypt the manifest before committing it, or apply it through your own Secret delivery tooling. Bootstrap that worker's Flux against its directory. The agent waits for the mounted token, enrolls once and stores its identity in the cluster. A consumed token can be removed from Git together with its resource entry. [Cluster enrollment](cluster-attachment.md) describes token renewal and enrollment status.

## Upgrade and verify

Bump the control-plane cohort's bundle digest, wait for Flux readiness, then update staging workers and production workers. The API supports agents at its own minor version and the preceding minor; workers must not run ahead of the API. `locoadm status` reports enrollment, heartbeats, DNS targets, version skew and unclaimed administrators from the API.

Revert the cohort pin to roll back platform software. Database migrations retain compatibility through a rollout; reverting a bundle does not roll back the database. Flux corrects drift and retries or rolls back failed chart upgrades.

Configure the reported DNS targets and wait for trusted application certificates. Verify an authorized environment on a worker that runs applications and builds:

```sh
locoadm verify --worker worker-staging --environment ENVIRONMENT_UUID
```

Verification builds disposable source, delivers a random secret, checks HTTPS and queries its logs. `--ca-file PATH` supplies a private CA for application checks. API and source-upload requests use system certificate roots. The command removes its disposable resource after success or failure; a process crash can leave a `verify-<UUID>` resource to remove.

## Export recovery material

```sh
locoadm recovery export --context control-plane --namespace loco-system \
  --output ./recovery-export --recipient MATERIAL_AGE_RECIPIENT \
  --share-recipient FIRST_CUSTODIAN_AGE_RECIPIENT \
  --share-recipient SECOND_CUSTODIAN_AGE_RECIPIENT
```

Supply one distinct recipient per configured recovery share, in share order, plus a separate material recipient. Match the chart's share count. `--seal-secret` defaults to `openbao-seal`; pass an empty name for KMS or transit auto-unseal. `--init-secret` defaults to `loco-control-plane-openbao-init`.

Only ciphertext reaches the new output directory. `material.age` contains the static seal key, when selected, and generate-root token; each `share-N.age` belongs to one custodian. Source Secrets remain intact. Verify restoration before removing source material. Preserve PostgreSQL backups, OpenBao snapshots, volumes and external-service backups through their respective backup procedures.
