# Platform releases

Each Helm chart and CRD bundle is versioned on its own and published as an immutable OCI artifact. A Flux OCI bundle, released by hand, pins chart, CRD bundle and image digests for worker clusters.

## Charts and CRD bundles

A chart's version is the `version` in its `Chart.yaml`. The Application, Build and Gateway CRD bundles take theirs from `tools/platform-release/crds.yaml`. The Gateway bundle holds the CRDs of the `gateway-helm` dependency `charts/loco-worker/Chart.yaml` pins. The cert-manager chart is mirrored at the upstream version `charts/cert-manager.yaml` pins. Charts set no `appVersion` and no default Loco image tag.

Bump a chart's version when a file the chart packages changes, and a CRD bundle's version when its CRD file or the `gateway-helm` dependency changes. Pull requests run these checks:

```sh
mise run lint:charts
mise run lint:crd-versions
```

`lint:charts` runs `ct lint`, which compares each chart's version with `origin/main` and skips files the chart's `.helmignore` excludes. `lint:crd-versions` compares against the merge base with `origin/main`; pass another ref as its argument for a stacked branch.

Every push to `main` that touches charts, CRDs or this tooling runs `mise run release:artifacts`. It publishes each version the registry does not hold yet and skips a version already published with the same content. A version already published with different content fails the run. The manifest annotation `io.loco.platform.content` records a digest of the packaged files, so repackaging the same chart compares equal.

## Platform bundle

Publish a bundle from a main commit whose image builds and provenance attestations have completed and whose chart and CRD bundle versions are published:

```sh
gh workflow run platform-release.yml --ref main \
  -f version=0.7.0-rc.1 \
  -f core_commit="$(git rev-parse origin/main)"
```

`version` is the bundle's own version and accepts `X.Y.Z` and `X.Y.Z-rc.N`, without a `v` prefix. The bundle pins the chart and CRD bundle versions in the source at `core_commit` by digest, and fails when one of them is not published. It publishes nothing else. Configure the `platform-release` GitHub environment to permit only `main`. Publishing requires package write access; it never connects to Kubernetes.

Artifacts use these repository paths under `ghcr.io/<owner>/<repository>`:

```text
charts/loco-worker
charts/loco-control-plane
charts/cert-manager
crds/applications
crds/builds
crds/gateway
platform
```

Install `loco-control-plane` separately in each control-plane region using its existing-Secret configuration. The worker-cluster Flux bundle records the chart artifact but does not install control-plane services on workers.

Set these GHCR packages to public. Artifact publication fails until every published chart and CRD bundle permits anonymous pulls, and the bundle requires anonymous access to every artifact and image it pins. An existing bundle version fails publication.

The bundle publisher verifies each reused `sha-<commit>` image's GitHub attestation against the main source commit and `build-push.yml` signer. Helm dependency versions come from the chart lockfiles.

Point an `OCIRepository` named `platform-bundle` in `flux-system` at the `platform` repository. Reconcile the bundle root with a Kustomization after cluster configuration is ready. The bundle creates `crds` and `platform-releases` Kustomizations and checks both current bundle revision and current CRD artifact revisions before deploying controllers.

The cluster configuration supplies `platform-reconciler` in `flux-system`, the `cert-manager` namespace, the `loco-system` namespace with the Pod Security labels of `manifests/namespaces/loco-system.yaml`, and these values sources for the `loco-worker` release:

| Release | ConfigMap | Secret |
| --- | --- | --- |
| loco-worker | loco-worker-values | loco-worker-secrets |

Each source has a `values.yaml` key. ConfigMaps and Secrets remain owned by the cluster configuration. The release creates the `loco-obs`, `loco-obs-node` and `loco-builds` namespaces itself. For a cluster's first agent start, the values set `agent.joinToken.existingSecret` to a Secret holding the join token `locoadm cluster create` printed. They must also set `global.region` to the region the cluster is registered under in the API and `global.exporter.endpoint` to the OTLP/HTTP endpoint the collectors export to; the chart fails to render without either. Bundle values override image references. No chart installs CRDs: the `crds` layer applies them. CRD Kustomizations disable pruning; each CRD also carries a prune protection annotation.

Validate packaging and failure paths without registry writes:

```sh
mise run test:platform-release
mise run test:platform-render
mise run lint:charts
mise run lint:crd-versions
mise run lint:actions
```
