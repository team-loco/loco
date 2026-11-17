# Platform releases

Each Helm chart and CRD bundle is versioned on its own and published as an immutable OCI artifact. A Flux OCI bundle, published with each release, pins chart, CRD bundle and image digests for control-plane and worker clusters.

## Charts and CRD bundles

A chart's version is the `version` in its `Chart.yaml`. The Application, Build and Gateway CRD bundles take theirs from `tools/platform-release/crds.yaml`. The Gateway bundle holds the CRDs of the `gateway-helm` dependency `charts/loco-worker/Chart.yaml` pins. The cert-manager chart is mirrored at the upstream version `charts/cert-manager.yaml` pins. Envoy Gateway is mirrored as its own chart at the `gateway-helm` version the worker chart pins. Charts set no `appVersion` and no default Loco image tag.

Bump a chart's version when a file the chart packages changes, and a CRD bundle's version when its CRD file or the `gateway-helm` dependency changes. Pull requests run these checks:

```sh
mise run lint:charts
mise run lint:crd-versions
```

`lint:charts` runs `ct lint`, which compares each chart's version with `origin/main` locally and the event base commit in CI, and skips files the chart's `.helmignore` excludes. `lint:crd-versions` compares against the merge base with `origin/main`; pass another ref as its argument for a stacked branch.

Every push to `main` that touches charts, CRDs or this tooling runs `mise run release:artifacts`. It publishes each version the registry does not hold yet and skips a version already published with the same content. A version already published with different content fails the run. The manifest annotation `io.loco.platform.content` records a digest of the packaged files, so repackaging the same chart compares equal.

## Releases

A release publishes one Loco version: the platform bundle `X.Y.Z` and release `vX.Y.Z`, whose assets are the `loco` and `locoadm` binaries, `checksums.txt` and the bundle's `release.json`. Nothing releases on push. Start one by hand from `main`:

```sh
gh workflow run release.yml --ref main \
  -f commit="$(git rev-parse origin/main)" \
  -f version=0.7.0 \
  -f summary="Dashboard sign-in works with plain-HTTP identity providers."
```

`summary` is required: plain prose saying what changed, which opens the release notes above the generated list of merged pull requests. `commit` defaults to the head of `main`; `version` takes `X.Y.Z` without a `v` prefix and defaults to the latest `v*` tag with its patch bumped. The binaries report `vX.Y.Z` as their version.

Before publishing anything, the workflow fails unless the commit is on `main`, tag `vX.Y.Z` does not exist, the `CI` push run for the commit succeeded, and every earlier first-parent commit since the latest `v*` tag passes `CI` (only the commit itself is checked when there is no tag). An earlier commit passes when its `CI` push run succeeded, when it has no run, or when its run failed and every job that failed succeeded in the `CI` push run of a later commit in that range. A run whose `Changes` job failed skips the checks after it, so only that run succeeding on a re-run clears it. `CI` skips checks whose paths a push did not change, such as the installation e2e suite, so a failed job that later pushes skip keeps blocking. A push of several commits runs `CI` once, on its head, over all of them. The failure lists each blocking commit with its run's conclusion and the failed jobs no later run passed; re-running a failed run clears it. It then builds the binaries and publishes the bundle, which fails when bundle version `X.Y.Z` exists, a pinned chart or CRD bundle version is not published, or a `sha-<commit>` image lacks a linux/amd64 or linux/arm64 manifest or a provenance attestation. Last, it creates release `vX.Y.Z` as a draft, uploads its assets and publishes it, which tags the commit. A run that fails after the bundle is published leaves no tag; rerun it with the next version.

The bundle pins the chart and CRD bundle versions in the source at the commit by digest. Its `release.json` records the release version, source commit, artifacts and images. Bundle publication accepts stable releases. Configure the `platform-release` GitHub environment to permit only `main`. Publishing requires package write access; it never connects to Kubernetes.

Artifacts use these repository paths under `ghcr.io/<owner>/<repository>`:

```text
charts/loco-worker
charts/loco-control-plane
charts/cert-manager
charts/gateway-helm
crds/applications
crds/builds
crds/gateway
platform
```

Flux 2.10.0 is the minimum supported version; `mise.toml` pins the CLI used for validation. Select `./control-plane`, `./worker` or `./all` as the Flux Kustomization path. The worker entry point installs only worker services; the combined entry point runs both roles on one cluster. Postgres is supplied by the operator.

Set these GHCR packages to public. Artifact publication fails until every published chart and CRD bundle permits anonymous pulls, and the bundle requires anonymous access to every artifact and image it pins.

The bundle publisher verifies each reused `sha-<commit>` image's GitHub attestation against the main source commit and `build-push.yml` signer. Helm dependency versions come from the chart lockfiles.

Point an `OCIRepository` named `platform-bundle` in `flux-system` at the `platform` repository. Reconcile its selected entry point. The bundle applies these layers with health checks and dependencies:

| Layer | Dependencies |
|---|---|
| `loco-namespaces` | None; creates namespaces and the `platform-reconciler` service account and cluster binding |
| `loco-crds` | Namespaces; retains CRDs and checks the expected artifact revision |
| `loco-infra` | CRDs; installs cert-manager and Envoy Gateway as separate releases |
| `loco-control-plane` | Infrastructure and operator-owned `loco-cluster-config` |
| `loco-worker` | Infrastructure and operator-owned `loco-cluster-config` |

The namespace layer uses Flux's bootstrap permissions to create the reconciler identity. The other layers use that service account. The operator's `loco-cluster-config` Kustomization depends on `loco-namespaces`, decrypts standalone Secrets and can install an issuer. Each internal dependency checks the bundle content revision, observed generation and Ready condition, including during uncommitted local builds from one commit.

Each Helm release accepts an optional `<release>-values` ConfigMap and `<release>-secrets` Secret, with a `values.yaml` key. Values sources are operator-owned; credentials belong in standalone Secrets referenced by name. Bundle values pin Loco images and disable the gateway subchart in the worker release. Envoy Gateway runs separately in `GatewayNamespace` mode. CRD Kustomizations disable pruning; each Loco CRD also carries prune protection.

Worker values set `agent.joinToken.existingSecret`, `global.region` and `global.exporter.endpoint` for enrollment and telemetry. Control-plane values supply the API environment Secret, identity provider, external Postgres, registry and object storage configuration. See `examples/gitops/` for the operator repository layout.

## Releases from source

`locoadm` installs from a local `release.json` when the installation file sets `release.manifest`. Two tasks print one for an unreleased commit.

`release:main` resolves a commit on `main` from what CI already published: the chart and CRD bundle versions its source pins and its `sha-<commit>` images. Without `--commit` it takes the newest of the last 50 first-parent commits of `origin/main` whose six images are all published. It verifies each image's attestation like the bundle publisher, needs `gh` signed in, and publishes nothing.

```sh
git fetch origin main
mise run release:main -- --repository team-loco/loco > release.json
```

`release:local` builds the checkout, including uncommitted changes, into a registry you run. It packages the charts and CRD bundles, builds the six images for the Docker daemon's platform and pushes the layered Flux bundle and its artifacts as version `0.0.0-local.g<commit>`, replacing an earlier push of that version. `--push` is the registry address on this machine, served over plain HTTP when it starts with `localhost:`. `--address` is the name the `release.json` gives the same registry, which both `locoadm` and the cluster's nodes must reach. The registry must allow anonymous pulls of the pushed repositories.

```sh
mise run release:local -- --repository team-loco/loco \
  --push localhost:15000 --address registry.example.localhost:15000 \
  --username push --password "$REGISTRY_PASSWORD" > release.json
```

Build `loco` and `locoadm` from the same commit, with the version the `release.json` names:

```sh
version=$(jq -r .version release.json)
for binary in loco locoadm; do
  go -C cmd build -ldflags "-X main.version=v$version" -o "../bin/$binary" "./$binary"
done
```

Validate packaging and failure paths without registry writes:

```sh
mise run test:platform-release
mise run test:platform-render
mise run lint:charts
mise run lint:crd-versions
mise run lint:actions
```
