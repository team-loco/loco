---
title: "Local development"
---

`mise run dev` installs Loco through `locoadm` on one kind cluster and runs the dashboard on the host. The cluster contains the control plane and one enrolled production worker; Postgres, Dex, the registry and the source bucket run in Compose.

Install mise and Docker, then use the pinned tools:

```sh
mise run setup
mise run dev
```

The configured admin is `dev@loco.test`, with password `loco-dev`. Device approval and worker attachment finish automatically; signup is open. The dashboard listens at `http://localhost:5173`, and its API is `http://api.loco-dev.localhost:8000`. Dex uses `http://dex.loco-dev.localhost:5556/dex`.

Dex offers GitHub login when `GH_OAUTH_CLIENT_ID` and `GH_OAUTH_CLIENT_SECRET` name an OAuth app with callback `http://dex.loco-dev.localhost:5556/dex/callback`. Team members can run `infisical login` once; `mise run dev` loads available project credentials for installation. Export the two variables for your own app when using another account. Password login works without them.

Worktrees claim slots 1 through 9. Slot N uses `loco-dev-N.localhost` and adds 100×N to the ports; slot 1 serves the API at `http://api.loco-dev-1.localhost:8100` and dashboard at `http://localhost:5273`. Run `mise run stack` for this checkout's URLs, private kubeconfig and state directory.

## Deploy platform changes

With `mise run dev` running, publish code or chart edits through the installer:

```sh
mise run dev:deploy
```

The task runs `release:local`, builds matching CLIs, applies the control plane and attaches the worker at that release. Helm owns the workloads. The host dashboard reloads UI edits through Bun; the installed dashboard image updates with `dev:deploy`.

The installation, release manifest, kubeconfig and file credentials live under the shared git directory's `loco-stacks/state/<slot>/`, outside the worktree. `mise run stop` retains them and stops the host dashboard, fixtures and kind node; `mise run start` resumes without publishing or installing again. Interrupted installation or deployment resumes against its retained database with `mise run dev`. Changed inputs stop ordinary startup with the reset instruction; `dev:deploy` explicitly applies changes to an installed stack. Fixture or cluster topology changes need `mise run dev:reset`, followed by `mise run dev`. Reset deletes this stack's cluster, volumes and credentials while retaining its slot. `mise run down` also releases the slot; run it before deleting a worktree.

`mise run stack:install` installs without starting the host dashboard. To inspect Kubernetes workloads, use the kubeconfig printed by `mise run stack`:

```sh
export KUBECONFIG=/path/from/mise-run-stack
kubectl -n loco-control-plane logs deployment/loco-control-plane-api --follow
kubectl -n loco-system logs deployment/loco-worker-agent --follow
k9s
```

Use an installed `k9s` for interactive inspection, or the pinned `kubectl` through `mise exec -- kubectl`.

## Connect the CLI

The wrappers use this stack's isolated credential home and file credential store:

```sh
mise run loco workspace list
mise run locoadm status
mise run locoadm cluster list
```

They preserve the developer's ordinary CLI login and kube context. The local worker targets production, matching dashboard and CLI defaults; use region `local` in application configuration.

Apps use `https://<name>.apps.loco-dev.localhost:8443`, with the slot suffix and port offset in worktrees. Accept the browser warning for self-signed certificates. Export the apps CA with:

```sh
mise run stack:ca > apps-ca.pem
```

`locoadm verify` targets installations that serve apps on port 443 and is not used against the local stack.

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

The unreleased installation suite provisions its own kind cluster and external fixture services, then uses `locoadm` for control-plane installation, the configured admin's sign-in under closed signup and worker attachment:

```sh
mise run e2e:installation --no-teardown
mise run e2e:installation --skip-build
```

The suite builds images for Docker's host architecture, or pulls the ones `E2E_IMAGES` names (such as `ghcr.io/team-loco/loco-*:ci-<commit>`, `*` standing for the image name), and publishes digest-pinned charts, images and CRDs to a fixture registry. An operator pod uses an explicit fixture kubeconfig; The operator signs in with `locoadm login --device`; the suite approves the code by calling the API's `ApproveDeviceLogin` with an ID token from the fixture Dex's password grant, the call the dashboard makes when a person approves. Generated installation credentials and worker identity follow the public commands. Application setup uses authenticated APIs, then `loco deploy` builds source and checks its HTTPS response. No SQL creates ordinary users, sessions or cluster registrations on this path.

The pinned cloud-provider-kind controller publishes real LoadBalancer addresses. Fixture CoreDNS records route control-plane names and wildcard application names to those addresses. The fixture CA verifies the HTTPS certificate; these names represent operator-managed DNS inside the disposable environment and do not test Internet DNS propagation.

Each checkout owns a separate cluster. `--no-teardown` preserves it for inspection; `--skip-build` reuses its images while rerunning the workflow. Without `--no-teardown`, success and failure both remove the cluster and its external fixtures. Unknown options or test filters fail before provisioning. The E2E workflow runs this suite as an independent job with its own clean environment.
