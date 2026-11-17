# Technical Design Document: Platform delivery with Flux

**Status:** Approved design.

Flux installs and upgrades Loco's platform software on control-plane and worker clusters. Each cluster reconciles its own configuration from an operator-owned repository. Core publishes versioned charts, images and an OCI platform bundle; CI holds no kubeconfig.

Customer workloads follow the API → agent → `Application` path. Flux owns platform resources, including their CRDs, and leaves customer applications to Loco's controllers.

## Ownership

| Concern | Owner |
|---|---|
| Charts, images, versioned platform bundle | Core, published per release |
| Cluster inventory, values, secrets, release pins | Each operator's repository |
| Installation, upgrades, drift correction | Flux on each cluster |
| Cluster provisioning, networking, CNI | Operator's provisioning tools or provider |
| Registration, join tokens, bootstrap secrets, verification, recovery | `locoadm` |
| Customer workloads | API, agent and application controllers |

Each cluster runs four Flux controllers and reads only its own directory. There is no management cluster holding credentials for the fleet. Core is agnostic about Flux bootstrap: operators can use `flux bootstrap` or the Flux Operator. The delivery tooling pins Flux and documents its minimum supported version.

## Operator repository

Self-hosters keep their own repository. Loco Cloud uses its private fleet repository for workers; its control plane stays on its hosted service. Moving that control plane into Kubernetes is a separate decision.

```text
releases/
  control-plane.yaml               control-plane bundle source
  workers-staging.yaml             staging cohort bundle source
  workers-production.yaml          production cohort bundle source
clusters/<name>/
  flux-system/                     Flux bootstrap output
  kustomization.yaml               selects a releases/ file and this cluster's loco.yaml
  loco.yaml                        bundle entry point and cluster-config reconciliation
  values/loco-control-plane.yaml   per-cluster chart values
  values/loco-worker.yaml
  config/
    *.yaml                         standalone Secret templates
    issuer.yaml                    optional operator-supplied issuer
```

Core ships a working control-plane and worker example in `examples/gitops/`. `locoadm init` renders that layout from flags and generates Loco's bootstrap secrets. Chart defaults provide shared values; operators override them per cluster. There is no shared values directory in core.

## Versioned bundle

Each release publishes `ghcr.io/team-loco/loco/platform:X.Y.Z`, an OCI artifact containing Flux objects that pin charts and images by digest. Clusters consume released bundles. The bundle has three entry points:

| Entry point | Purpose |
|---|---|
| `./control-plane` | Control-plane cluster |
| `./worker` | Worker cluster |
| `./all` | Combined control plane and worker |

Flux applies these layers in dependency order and checks health before proceeding:

| Layer | Contents | Dependencies |
|---|---|---|
| `loco-namespaces` | `loco-system` with Pod Security labels, `cert-manager`, reconciler service account | None |
| `loco-cluster-config` (operator-owned) | Decrypted Secrets, optional issuer | Namespaces |
| `loco-crds` | Gateway API, Envoy Gateway and Loco Application/Build CRDs | Namespaces |
| `loco-infra` | cert-manager and a separate Envoy Gateway release | CRDs |
| `loco-control-plane` | API, UI, ingest proxy, OpenBao, ClickHouse | Infrastructure and cluster config |
| `loco-worker` | Agent, controllers, collectors | Infrastructure and cluster config |

CRDs are never pruned. Changes must be additive because worker clusters upgrade independently. Envoy Gateway has its own chart and release; the worker bundle disables the `gateway-helm` subchart. Third-party versions remain pinned in tracked chart metadata.

Each release accepts an optional `<release>-values` ConfigMap and an optional values Secret. Image digests stay pinned in the bundle. Credentials are standalone Secrets referenced by name, never Helm values. The bundle has no `configVersion` field.

The control-plane bundle supports external ClickHouse and external OpenBao. Postgres is operator-supplied and sits outside the bundle. The worker entry point accepts an API endpoint without assuming where the control plane runs. Registry authentication defaults to token mode; static credentials remain supported.

The CNI must run before Flux and enforce NetworkPolicy. Provisioning owns it.

## Chart configuration and setup

Both Loco charts publish `values.schema.json`. Typed values expose operator-tuned API settings: `api.limits.*` maps to the `LOCO_*` count-limit settings in `api/config.go` and internal limits; `api.defaults.*` and retention settings expose their corresponding API configuration. Chart lint and delivery validation reject invalid values.

Setup Jobs have `ttlSecondsAfterFinished`. The OpenBao setup Job initializes OpenBao and configures its API access. A ClickHouse credentials Job creates missing user credentials and preserves existing credentials across retries and upgrades. Chart consumers read those credentials from Secrets.

The first administrator is listed in `api.systemAdmins` and claims access by signing in with `locoadm login`. DNS records belong to the operator or external-dns; `locoadm status` reports the targets.

## Secrets

Core has no SOPS dependency. `locoadm init` writes plaintext Kubernetes Secret files and `locoadm cluster create -o secret` prints a Secret manifest. Operators encrypt these manifests before committing, or deliver the named Secrets through External Secrets or another tool. The generated README documents this boundary.

With SOPS, operators install it themselves, select cluster and offline recipients, and configure Flux decryption. The cluster decryption key and Git deploy key are bootstrap inputs outside reconciliation. Core consumes plain Kubernetes Secrets by name.
| Secret | Created by | Delivery and custody |
|---|---|---|
| Database URLs, source bucket keys, control-plane registry credentials | Operator | Operator secret delivery |
| OpenBao static seal key | `locoadm init` | Operator secret delivery and offline custody |
| OpenBao KMS/transit credentials | Operator | Operator secret delivery or workload identity |
| OpenBao recovery shares and root token | Chart setup Job | Cluster custody; `locoadm recovery export` for offline custody |
| ClickHouse users and passwords | Chart credentials Job | Generated in cluster if missing |
| DNS-01 token | Operator | Operator secret delivery |
| Worker join token | `locoadm cluster create` | Direct Kubernetes Secret application |
| Agent identity key | Agent | Cluster Secret; never Git |
| Worker registry pull/push credentials | Operator, static mode only | Operator secret delivery; token mode needs none |
| Ingest token | API, fetched by agent | Never Git |

`locoadm` generates the static seal key and join tokens. OpenBao auto-unseals with a static key by default; KMS and transit are supported alternatives. Retain the seal key with recovery material because snapshots require it.

## Enrollment

Join tokens are single-use and stored by the API only as hashes. `CreateCluster` and `IssueJoinToken` accept an optional TTL. The default is one hour; `LOCO_CLUSTER_JOIN_TOKEN_MAX_TTL` caps requested lifetimes, such as seven days.

```sh
locoadm cluster create --name worker-staging --region example-east --tier staging \
  --kube-context worker-staging
```

The command prints a plaintext Secret manifest for direct application to the worker. Direct application keeps tokens outside Git by default. An unenrolled agent reads the token from a mounted file and retries with backoff while waiting for delivery or successful enrollment. It retains its identity after enrollment. Cluster list and status expose enrollment state. The default one-hour token TTL covers immediate application.

An approval-based join flow is outside this design. Pulling a token from the API would itself require a credential.

## Operator commands

| Command | Responsibility |
|---|---|
| `init` | Scaffold the repo once from flags, generate a seal key and write plaintext Secret files with encryption guidance |
| `cluster create`, `cluster join-token` | Register workers and deliver tokens with `--kube-context`; accept TTL and optional `-o secret` output |
| `cluster list/pause/resume/remove/restore` | Manage API cluster records |
| `login` | Sign in using `--api` and `--web` |
| `status` | Query the API for clusters, heartbeats, version skew, unclaimed admins and DNS targets |
| `verify` | Run the end-to-end probe using an image pinned in `locoadm` |
| `recovery export` | Export recovery material using `--context` and `--namespace` |
| `admin`, `user`, `org` | Manage API records |

`locoadm` has no `apply`, `plan`, `cluster attach` or `validate` command, and no `loco-install.yaml` format. Flux provides reconciliation and diff/build tools. Installation state ConfigMaps, installer locks, ownership annotations, release guards and resolution, prerequisite installers, issuer checks and value templates are outside `locoadm`'s responsibilities. Each Flux reconciles its own cluster; enrollment binds an agent's identity to its API cluster record.

## Upgrades and rollback

Release source files define rollout cohorts. Upgrade the control plane first, then staging workers, then production workers. Flux cannot enforce ordering across clusters; operators gate each cohort on the preceding cohort's health. Flux notifications can report reconciliation health as a commit status.

Staging cohorts can follow plain release semver ranges automatically. Production pins an exact digest and changes it through a reviewed PR. Core publishes no release-candidate bundles. Image automation is unnecessary because each bundle pins its images.

API minor version N supports agents at N and N−1. Workers must never run ahead of the API; status flags skew. Database migrations run at API startup and remain backward compatible through a rollout: add first, remove in a later release.

Helm releases wait for health, retry failed upgrades and roll back on failure. Layers depend on healthy predecessors. Operators roll back by reverting the bundle version in Git. Database migrations do not roll back, so schema compatibility is required. Flux corrects drift within its inventory; tenant resources remain outside that inventory.

## Local development

Local development uses Flux in kind with OCI sources in the local registry. Uncommitted changes are included in the bundle; no Git commit is needed.

```sh
mise run dev
mise run dev:deploy
```

`dev` starts kind and the compose fixtures for Postgres, registry, S3 and Dex, installs pinned Flux controllers, builds and pushes the platform bundle with `release:local`, and pushes `dev/flux/local/` as an OCI artifact containing values and plaintext development Secrets. It waits for Flux health, signs in automatically, and registers and enrolls the worker. The UI runs on the host at port 5173 for hot reload. Fixtures, automatic login and the state directory use the local-stack harness.

`dev:deploy` rebuilds changed images, pushes a bundle and reconciles Flux. `dev:deploy --suspend` pauses Flux for a release to permit direct Helm iteration. Local development exercises the same bundle layers and dependencies as deployed clusters.

## Validation

Chart and bundle changes run `flux build` over `examples/gitops/`, render chart values with their schemas, and validate resources with kubeconform, including schemas for the installed CRDs. CI needs neither a kubeconfig nor decryption keys; examples contain plain placeholder Secrets.

The installation e2e test installs bundle N−1 through Flux, upgrades to N, and checks that the agent identity key, OpenBao data and ClickHouse credentials survive. It then exercises login, cluster creation and verification. Local validation checks startup, automatic login and enrollment, changed-image deployment and Flux reconciliation.

## Core implementation order

1. Document this delivery design and the join-token and auto-unseal contracts.
2. Add agent enrollment waiting and issue-time join-token TTL with a server cap.
3. Add chart credential generation, separate gateway delivery, setup Job cleanup and typed schemas.
4. Publish layered bundles and entry points; push bundles from `release:local`.
5. Ship and validate `examples/gitops/` with a control plane and worker.
6. Implement the operator commands and flag-based connection inputs.
7. Exercise Flux installation and N−1-to-N state preservation.
8. Run local development through Flux and retain the local-stack fixtures and automatic login.
9. Delete installer commands, implementation and schema; document installation, self-hosting and registration.

The demo uses a `gitops/` directory and the same local workflow. Fleet provisioning and pinned tooling remain operator concerns; fleet configuration consumes the worker entry point, per-cluster values, join tokens and cohort release sources.
