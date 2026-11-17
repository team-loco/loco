---
title: "Local development"
---

The local environment runs Loco's control plane and cluster components for development. Install mise and Docker or OrbStack, then use the pinned repository tools:

```sh
mise run setup
```

Tool versions live in `mise.toml` and `mise.lock`. Start the platform and host dashboard with:

```sh
mise run dev
```

`dev` checks Docker with `mise run doctor` before starting the platform. If `DOCKER_HOST` is unset, it uses the OrbStack socket when available. Team members can run `infisical login` once to supply `GH_OAUTH_CLIENT_ID` and `GH_OAUTH_CLIENT_SECRET` for Dex’s GitHub connector. These variables can also be exported directly. Without credentials, the stack starts with password login and prints a message about the missing GitHub button.

`dev` runs `locoadm init --single-cluster` on the first start to generate an operator repository under the stack state directory’s `repo/`. It deep-merges `dev/flux/values/loco-control-plane.yaml` and `loco-worker.yaml` into the generated per-release values. Each publish merges current fixture overrides into those values and preserves fields outside the overrides. Generated Secrets persist across starts. Flux runs in kind and pulls the platform bundle and generated repository from the local registry. Postgres, Dex, the registry and source bucket use `dev/compose.local.yaml`. OpenBao and ClickHouse run in Kubernetes. Device login and worker enrollment run automatically. Sign in as `dev@loco.test` with password `loco-dev`.

Each worktree receives an isolated stack slot, private kubeconfig and credential home. Run `mise run stack` for its URLs. Slot N adds 100×N to the base ports: API 8000, dashboard 5173, Dex 5556 and apps 8443. Platform state and the image cache live beneath the shared git directory's `loco-stacks/state/<slot>/`.

```sh
mise run dev:deploy
mise run locoadm status
mise run loco whoami
```

`dev:deploy` rebuilds images whose Docker inputs changed, pushes the bundle, updates the generated `releases/control-plane.yaml` digest, publishes the repository and reconciles Flux. Uncommitted changes are included. `--suspend` pauses platform releases for manual Helm iteration; resume them through Flux before returning to automatic reconciliation.

`mise run stop` keeps state while stopping the dashboard, fixtures and kind node. `mise run dev` starts them. `mise run down` deletes the stack and frees its slot. Apps use self-signed certificates; export the CA with `mise run stack:ca > apps-ca.pem`. The application verification command requires port 443 and is exercised by installation acceptance.

## Charts

A chart change starts with a [helm-unittest](https://github.com/helm-unittest/helm-unittest) test that fails on the current chart. [`charts/TESTING.md`](https://github.com/team-loco/loco/blob/main/charts/TESTING.md) describes the static and unit tests and how to run one suite. Run both before opening a pull request:

```sh
mise run lint:charts
mise run test:charts
```

## Tasks

```sh
mise tasks
mise run test:cli
mise run lint:go
mise run docs:serve
```

Use the repository's tasks for builds, tests, code generation, and linting so local tools match CI. See [documentation maintenance](documentation.md) to edit this site.

## Installation end-to-end suite

The installation suite uses Flux to install the merge-base bundle and upgrade to the head bundle, checks retained identities and encryption keys, then exercises login, enrollment, source deployment, verification and recovery.

```sh
mise run e2e:installation --no-teardown
mise run e2e:installation --skip-build
```

The installation fixture runs `locoadm init --single-cluster` and merges `tests/e2e/fixtures/installation/values/` overrides into the generated repository. The fixture registry holds that repository and digest-pinned charts, images and CRDs. The baseline and head use the current bundle tooling. Each checkout owns its kind cluster; `--no-teardown` keeps it for inspection and `--skip-build` reuses its images. Default cleanup removes fixtures after success or failure.
