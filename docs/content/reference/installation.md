---
title: "Installation"
---

A Loco installation uses Flux to reconcile a versioned platform bundle and an operator repository containing cluster values and operator-delivered Secrets. The operator supplies the cluster network, and PostgreSQL unless the bundle runs it on CloudNativePG.

## Prepare infrastructure

Provide Kubernetes with its CNI, PostgreSQL or a StorageClass for the [bundled PostgreSQL](#bundled-postgresql), an OIDC provider, an S3-compatible source bucket, a registry or a volume or bucket for the [bundled registry](#bundled-registry), and DNS. Builds fail unless the CNI enforces Kubernetes NetworkPolicy. Allow the cluster to pull the platform charts, images and OCI artifacts. The [GitOps example](https://github.com/team-loco/loco/tree/main/examples/gitops) documents the minimum Flux version and the input Secrets.

Each cluster runs its own Flux and reads its own directory. The control plane uses `./control-plane`, a worker uses `./worker`, and a single-cluster installation uses `./all`. Keep the staging and production installations of Loco on separate clusters. A team’s user environments can share worker clusters within one installation.

### Register the dashboard with your identity provider

Register an OpenID Connect client for authorization code with PKCE. Set `WEB_URL` to the dashboard origin, such as `https://app.example.com`, and register that origin followed by `/auth/callback` as the redirect URI. Use a provider that verifies administrator email addresses.

Set `web.clientId` on the dashboard issuer in the API environment Secret's `AUTH_ISSUERS` entry. A public PKCE client can use its existing registration if the provider allows a server to exchange the code. For a confidential client, also set `web.clientSecret` in the Secret.

- **Microsoft Entra ID.** Move the existing callback URI from **Single-page application** to **Web** in the app registration, then set `web.clientSecret`. Use the tenant-specific issuer. [Microsoft authorization code flow](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-auth-code-flow)
- **Google.** Use a **Web application** client and set `web.clientSecret`. [Google OpenID Connect setup](https://developers.google.com/identity/openid-connect/openid-connect)

The API exchanges the sign-in code and sets Loco session cookies. The control-plane chart routes dashboard RPCs through `/api` on the dashboard origin.

## Scaffold the repository

Supply a published bundle digest and an environment file containing operator-supplied API credentials:

```sh
locoadm init --output ./fleet \
  --api https://api.example.com --web https://app.example.com \
  --domain apps.example.com --region us-ca-1 --admin admin@example.com \
  --bundle oci://ghcr.io/team-loco/loco/platform@sha256:BUNDLE_DIGEST \
  --api-env-file ./api.env
```

`--domain` is the app domain services are named under. For a control plane and worker on one cluster, add `--single-cluster --name local`. Bootstrap Flux against `clusters/local/`; the combined base selects `./all` and supplies both releases’ values.

`init` generates a static OpenBao seal key and writes it and the API environment as plaintext Secret files with mode `0600`. Encrypt these files before committing, or deliver the named Secrets through External Secrets. Core has no SOPS dependency; the generated README documents operator-managed encryption and Flux decryption. Join tokens are applied directly to worker clusters. Review the generated values and supply storage, database, OIDC, registry and DNS credentials, and the [certificate solvers](#configure-certificate-solvers), before reconciliation. Chart defaults define shared settings; each cluster overrides them in `values/`.

The repository has `releases/control-plane.yaml`, staging and production worker cohorts, and shared role defaults under `bases/`, and one directory under `clusters/` per cluster. Each cluster kustomization selects exactly one release cohort file, references a role base and provides a small values file with endpoints, domains, administrator and region settings. Staging can follow stable releases through a semver range. Production pins an exact bundle digest.

### Bundled PostgreSQL

`--postgres` runs the API database on a CloudNativePG cluster in the control-plane namespace instead of an operator-supplied `DATABASE_URL`. It adds the Flux Kustomization `loco-postgres` to `bases/control-plane/loco.yaml`, which applies the bundle's `./postgres-operator` entry point: the CloudNativePG operator in `cnpg-system`. `loco-cluster-config` then waits for `loco-postgres-operator`, so the control-plane release starts after the operator's CRDs and webhooks exist. It also sets `postgres.enabled: true` in the control-plane base values. Leave `DATABASE_URL` out of the API environment file; the API reads it from the cluster's `loco-control-plane-postgres-app` Secret.

The cluster runs two instances on 10Gi volumes of the default StorageClass. Set `postgres.instances`, `postgres.storage` and `postgres.resources` in the control-plane values to change them. The bundled PostgreSQL has no backups yet; back it up yourself, for example with `pg_dump`. The [control-plane chart README](https://github.com/team-loco/loco/blob/main/charts/loco-control-plane/README.md#bundled-postgresql) describes extra databases for other services. Without `--postgres`, add the same Kustomization, dependency and value to an existing repository to switch to the bundled database; the new database starts empty.

### Bundled registry

`--registry registry.example.com` runs a zot registry for build images in the control plane, served at that hostname through the control-plane Gateway with the same certificate as the API. It sets `registry.enabled` and `registry.hostname` in the control-plane cluster values and the registry hostname in `api.registry.service` and the worker's `LOCO_REGISTRY_HOST`. The control-plane base already sets `api.registry.auth: token`, and the worker base `registry.auth: token`, which the bundled registry requires: workers and builds get short-lived tokens from the API, and no registry password exists. Leave `LOCO_REGISTRY_USERNAME` and `LOCO_REGISTRY_PASSWORD` out of the API environment file. Point the hostname's DNS at the control-plane Gateway, and add it to the DNS-01 solver's zone. Images go to a 50Gi volume of the default StorageClass; for an S3-compatible bucket, set `registry.storage.type: s3` and `registry.storage.s3` as the [control-plane chart README](https://github.com/team-loco/loco/blob/main/charts/loco-control-plane/README.md#bundled-registry) describes.

### Configure certificate solvers

Both charts create a Let's Encrypt ACME ClusterIssuer by default and fail to render until `certManager.issuer.solvers` holds cert-manager's [solver list](https://cert-manager.io/docs/configuration/acme/#solvers). Loco assumes no DNS provider. The worker Certificate covers wildcard names, which ACME issues only through DNS-01, so configure a DNS-01 solver for your DNS provider. The generated values set `solvers: []`, and both charts refuse to render until you replace it with your provider's solver, as in this Cloudflare example:

```yaml
certManager:
  issuer:
    email: admin@example.com
    solvers:
      - dns01:
          cloudflare:
            apiTokenSecretRef:
              name: cloudflare-api-token
              key: api-token
```

The charts create no solver credentials. Deliver any Secret a solver references to the `cert-manager` namespace with the other cluster Secrets. To skip ACME, set `certManager.issuer.create: false` with the `name` and `kind` of an existing issuer, or serve your own TLS Secret with the worker's `certManager.existingTLSSecret` or the control plane's `gateway.tlsSecretName`.

## Bootstrap Flux

Each cluster needs Flux 2.10 or later. Bootstrap Flux against the cluster's directory with your Git host's supported authentication. For example:

```sh
flux bootstrap git --url=ssh://git@example.com/operator/fleet \
  --branch=main --path=clusters/control-plane --private-key-file=./deploy.key
```

Use a different deploy key on each cluster. If you choose SOPS, install it through your own tooling, encrypt the Secret files, configure `spec.decryption` on `loco-cluster-config`, and provision the cluster key in `flux-system` before reconciliation. Keep an offline recipient for recovery. External Secrets can supply the same named Secrets without Flux decryption.

The bundle supplies namespaces and the reconciler service account. Its namespace layer runs first; the operator's `loco-cluster-config` applies Secrets and optional issuers. Namespaces and CRDs are never pruned. Infrastructure installs cert-manager and an independent gateway release. The opt-in `./postgres-operator` entry point installs the CloudNativePG operator after the namespaces. Control-plane and worker releases wait for infrastructure and cluster configuration.

Each HelmRelease reads an optional `<release>-base-values` ConfigMap before an optional `<release>-values` ConfigMap and `<release>-secrets` Secret in `flux-system`, with a `values.yaml` key. Standalone credential Secrets live in the workload namespace and are referenced by name. Image digests remain pinned inside the bundle. The control-plane chart renders ClickHouse credential Secrets, reads existing data through Helm `lookup`, and retains those Secrets on uninstall. OpenBao initialization and auto-unseal run through chart Jobs.

## Sign in and enroll workers

```sh
locoadm login --api https://api.example.com --web https://app.example.com
locoadm status
```

Sign in with a verified email listed in `api.systemAdmins` to claim the first system administrator. `--device` supports approval from another machine. `loco` and `locoadm` share the resulting session.

Bootstrap each worker’s Flux against its directory first. Once the bundle creates `loco-system`, register the worker and deliver its one-time join-token Secret directly:

```sh
locoadm cluster create --name worker-staging --country us --subdivision ca --kube-context worker-staging
```

Keep the join token out of Git. The default one-hour TTL covers immediate application. `--kube-context` applies the Secret through the Kubernetes client and prints only a confirmation. The agent waits for the mounted token, enrolls once and stores its identity in the cluster.  [Cluster enrollment](cluster-attachment.md) describes token renewal and enrollment status.

For fully unattended installation, an encrypted join-token file in the worker configuration is an option. Set a longer `--ttl`, encrypt it with operator tooling and configure Flux decryption; remove the spent file after enrollment.

## Upgrade and verify

Bump the control-plane cohort's bundle digest, wait for Flux readiness, then update staging workers and production workers. The API supports agents at its own minor version and the preceding minor; workers must not run ahead of the API. `locoadm status` reports enrollment, heartbeats, version skew and unclaimed administrators from the API.

Revert the cohort pin to roll back platform software. Database migrations retain compatibility through a rollout; reverting a bundle does not roll back the database. Flux corrects drift and retries or rolls back failed chart upgrades.

Read the worker gateway’s load balancer address from Kubernetes or its provider:

```sh
kubectl --context worker-staging get gateway -n loco-system
```

Point the application wildcard, such as `*.apps.example.com`, at that address once, or run external-dns to manage the record. Wait for trusted application certificates. Verify an authorized environment on a worker that runs applications and builds:

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
