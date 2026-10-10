# Platform releases

The platform release workflow publishes immutable Helm charts, Application and Build CRDs, Gateway CRDs, and a final Flux OCI bundle. Each bundle records the source commit and exact chart, CRD and image digests.

Publish a release from a main commit whose image builds and provenance attestations have completed. The commit must contain the platform release tooling:

```sh
gh workflow run platform-release.yml --ref main \
  -f version=0.7.0-rc.1 \
  -f core_commit="$(git rev-parse origin/main)"
```

`version` accepts `X.Y.Z` and `X.Y.Z-rc.N`, without a `v` prefix. Configure the `platform-release` GitHub environment to permit only `main`. Publishing requires package write access; it never connects to Kubernetes.

Artifacts use these repository paths under `ghcr.io/<owner>/<repository>`:

```text
charts/loco-core
charts/loco-networking
charts/loco-obs
charts/loco-operator
charts/cert-manager
crds/applications
crds/builds
crds/gateway
platform
```

Set these GHCR packages to public before publishing a deployable bundle. The workflow verifies anonymous artifact and image access before publishing the bundle, then verifies anonymous bundle access. Initial package creation can stop before bundle publication while package visibility is configured; use a new version for the next attempt. Every existing version fails publication, including partially published versions.

The publisher verifies each reused `sha-<commit>` image's GitHub attestation against the main source commit and `build-push.yml` signer. Helm dependency versions come from the existing chart lockfiles and the cert-manager pin in `helmfile.yaml.gotmpl`. The bundle publishes last, after all individual artifacts succeed.

Point an `OCIRepository` named `platform-bundle` in `flux-system` at the `platform` repository. Reconcile the bundle root with a Kustomization after cluster configuration is ready. The bundle creates `crds` and `platform-releases` Kustomizations and checks both current bundle revision and current CRD artifact revisions before deploying controllers.

The cluster configuration supplies `platform-reconciler` in `flux-system`, namespaces `loco-system`, `loco-builds`, `observability` and `cert-manager`, and these values sources:

| Release | ConfigMap | Secret |
| --- | --- | --- |
| loco-core | loco-core-values | loco-core-secrets |
| loco-operator | loco-operator-values | none |
| loco-obs | loco-obs-values | loco-obs-secrets |

Each source has a `values.yaml` key. ConfigMaps and Secrets remain owned by the cluster configuration. Bundle values override image references and disable operator chart CRDs with `crd.enable: false`. CRD Kustomizations disable pruning; each CRD also carries a prune protection annotation.

Validate packaging and failure paths without registry writes:

```sh
mise run test:platform-release
mise run test:platform-render
mise run lint:actions
```
