# Local development

The local environment runs Loco's control plane and cluster components for development. Install mise and Docker or OrbStack, then use the pinned repository tools:

```sh
mise run setup
```

Tool versions live in `mise.toml` and `mise.lock`. The local API reads its configuration from `dev/api.env`, which holds only dev values, so a fresh clone needs no `.env` file.

```sh
mise run tilt
```

Tilt starts the local cluster and services. The API listens at `http://localhost:8000`, and the dashboard at `http://localhost:5173`. The dashboard signs in through Dex at `http://localhost:5556/dex` as `dev@loco.test` with the password `loco-dev`.

Dex also offers GitHub login when `GH_OAUTH_CLIENT_ID` and `GH_OAUTH_CLIENT_SECRET` name a GitHub OAuth app whose callback URL is `http://localhost:5556/dex/callback`. Team members with access to the Loco Infisical project log in once, and `mise run tilt` then runs Tilt under `infisical run`, which supplies both:

```sh
infisical login
mise run tilt
```

Anyone else can export the two variables for their own OAuth app before `mise run tilt`. Without either, the stack starts without the GitHub button and `mise run tilt` prints a line saying so.

Each git worktree gets its own stack, so several can run at once. A worktree takes the first free slot N from 1 to 9, and its ports move up by 100×N: slot 1 serves the API at `http://localhost:8100` and the dashboard at `http://localhost:5273`. Run `mise run stack` for the current checkout's URLs, and `mise run down` to delete its stack before removing the worktree.

## Connect the CLI

```sh
mise run build
./bin/loco login --host http://localhost:8000
./bin/loco config set webHost http://localhost:5173
```

Tilt starts development components and supports ordinary login. The complete first-admin path uses the [installation workflow](../operations/self-hosting.md#first-system-admin); it requires installer-managed destination state. The installation e2e suite below exercises that workflow from a fresh cluster.

## Attach the local cluster

The local agent enrolls with a join token. With an account that has `system:admin`, create the cluster, stop Tilt, and start it again with the printed token in `LOCO_JOIN_TOKEN` so it redeploys `loco-worker` with it:

```sh
mise run build:locoadm
./bin/locoadm cluster create --name loco-local --region us-east-1 --tier production
mise run stop
LOCO_JOIN_TOKEN=<printed token> mise run tilt
```

The agent keeps its key in the `loco-worker-agent-key` Secret, so the token is needed again only after the kind cluster or that Secret is deleted.

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

The unreleased installation suite provisions its own kind cluster and external fixture services, then uses `locoadm` for control-plane installation, closed-signup first-admin authentication and worker enrollment:

```sh
mise run test:e2e-installation-runner
mise run e2e:installation --no-teardown
mise run e2e:installation --skip-build
```

The first command tests failure propagation, cleanup and the real HTTP login driver without a cluster. The cluster suite builds images for Docker's host architecture and publishes digest-pinned charts, images and CRDs to a fixture registry. An operator pod uses an explicit fixture kubeconfig; Dex authentication, generated installation credentials and worker identity follow the public commands. Application setup uses authenticated APIs, then `loco deploy` builds source and checks its HTTPS response. No SQL creates ordinary users, sessions or cluster registrations on this path.

The pinned cloud-provider-kind controller publishes real LoadBalancer addresses. Fixture CoreDNS records route control-plane names and wildcard application names to those addresses. The fixture CA verifies the HTTPS certificate; these names represent operator-managed DNS inside the disposable environment and do not test Internet DNS propagation.

Each checkout owns a separate cluster. `--no-teardown` preserves it for inspection; `--skip-build` reuses its images while rerunning the workflow. Without `--no-teardown`, success and failure both remove the cluster and its external fixtures. Unknown options or test filters fail before provisioning. The E2E workflow runs this suite as an independent job with its own clean environment.
