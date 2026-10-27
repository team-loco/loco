# 🚂 Loco

> Deploy containerized apps right from your terminal.

Loco is a container orchestration platform that simplifies application deployment. Run `loco deploy` and Loco handles the rest - building, deploying, and scaling your applications on Kubernetes.

## Features

- **Simple deployments** - Expose your app to the internet with just `loco deploy`!
- **Simple Configuration** - Configure your services in a `loco.yaml` file. A sample with sensible defaults can be generated via `loco infra init`.
- **HTTPS by default** - Automatic SSL certificate management, powered by Let's Encrypt and Certificate Manager.
- **Fast and Secure** - Envoy Gateway API serves HTTP3 traffic, handles TLS termination, and routing.

## Quick Start

1.  **Download the loco cli**

```bash
curl -fsSL https://loco.build/install.sh | sh
```

This installs the latest release to `~/.local/bin` and verifies its checksum. The installer supports Linux and macOS on amd64 and arm64, and accepts either `curl` or `wget` for downloads. Set `LOCO_INSTALL_DIR` or pass `--bin-dir` to choose a different directory. Set `LOCO_VERSION` or pass `--version` to install a specific release tag.

If `~/.local/bin` is not already on your `PATH`, the installer prints the command to add it for your shell.

Run `loco update` to replace the installed binary with the latest release.

2. **Log in with `loco login`.** It opens the dashboard in your browser, where the instance's identity provider signs you in, then returns to the terminal. On a machine without a browser, `loco login --device` prints a code to enter on another device.
3. **Run `loco deploy`** from your application directory to deploy one service with API defaults. The CLI turns the directory name into a safe service name and assigns a public hostname with a four-letter suffix under the API's platform domain. It uses the default region and resources, and `Dockerfile`. In a terminal it asks for the service name. For agents and CI, pass `--name`, any needed `--port`, `--region`, `--domain`, `--dockerfile` or `--context` flags, and `--yes`. Use `--private` to omit a public domain or `--image` to deploy a public image without building source.
4. **Use `loco.yaml` for declarative configuration.** Run `loco infra init` to create it and `loco infra validate` to check it. From the directory that holds the file, `loco deploy` shows a plan, packs the source, uploads it, builds each source service, and applies the file with the builds. The tarball honors `.dockerignore` and never contains `.git`, `.env` or `.env.*`. Ctrl-C during a build detaches without canceling it. A service with `image` set deploys that public image without a build. `loco deploy web worker` builds only the named services.

Apps run under Kubernetes' `restricted` Pod Security profile, so the image must run as a numeric non-root user (for example `USER 10001` in the Dockerfile).

Your app will be available at the first of its `domains`, such as `https://<app-name>.onloco.app`.
`loco resource` holds `status`, `logs`, `events`, `env`, `scale` and `destroy`. `loco builds` lists builds and shows, follows or cancels one: `loco builds list`, `loco builds get <id>`, `loco builds logs <id> -f` and `loco builds cancel <id>`.

See all loco cli commands via `loco help`.
Loco also generates completions for shells such as bash and zshrc.

```bash
loco completion zsh
```

## Examples

Deployable sample apps, each with its own `loco.yaml`:

- [`examples/test-api`](./examples/test-api/): a `backend`, `auth` and `frontend` service
- [`examples/metrics-validation`](./examples/metrics-validation/): generates CPU, memory, network and disk load to check metrics

## How-Tos

- how we can scale our clusters

## Under the Hood

### CLI

- **Cobra:** For building the command-line interface.
- **Charm's libraries (e.g., Bubble Tea, Lipgloss):** For rich terminal UI components.

### API Server

- **Go:** The primary language for the backend API.
- **Connect RPC:** For communication between the CLI and the backend API.
- **PostgreSQL:** Database for user and deployment information.

### Kubernetes Setup

- **CNI:** Any CNI that enforces Kubernetes NetworkPolicy, which isolates workspaces from each other.
- **Envoy:** Implements the 'new' Kubernetes Gateway API. Responsible for routing, TLS termination, enabling HTTP3
- **cert-manager:** For automatic SSL certificate management for various components. (Let's Encrypt).
- **OpenTelemetry:** For observability; collects metrics, logs and traces.
- **ClickHouse:** As the data store for observability data, read through the observability proxy.

## Sign-up Policy

The API decides who can create an account the first time they sign in through the instance's identity provider; existing accounts are not affected. `AUTH_SIGNUP_MODE` selects the policy: `open` (the default) accepts every identity, `domains` accepts only verified email addresses whose domain is in the comma-separated `AUTH_SIGNUP_DOMAINS` list, and `closed` creates no new accounts. The API refuses to start with an unknown mode or with `domains` and an empty list.

## Documentation

- **API Documentation:** [https://buf.build/team-loco/loco](https://buf.build/team-loco/loco) - Browse proto files, generate client SDKs, and view API documentation.

## Contributing

Every tool the repository uses (Go, bun, the linters, code generators, helm, kind and so on) is pinned in [`mise.toml`](./mise.toml), with exact versions and checksums for each platform in `mise.lock`. CI installs from the same files. Install [mise](https://mise.jdx.dev/getting-started.html), then from the repository root:

```bash
mise run setup
```

That installs the pinned tools, the web dependencies and the git hooks, then runs `mise run doctor`, which checks that Docker (OrbStack or Docker Desktop) and Docker Compose are running and recent enough. Docker and mise are the only tools you install yourself. With [`mise activate`](https://mise.jdx.dev/getting-started.html#activate-mise) in your shell, the pinned tools are on `PATH` whenever you are inside the repository.

Builds, tests, code generation, linting and the local environment are mise tasks, which CI and the hooks run as well. `mise tasks` lists them.

`mise run tilt` brings up the local environment, with the API on `http://localhost:8000` and the web UI on `http://localhost:5173`. The web UI signs in through [Dex](https://dexidp.io) on `http://localhost:5556/dex`, configured in `dev/dex.yaml`, which defines the local user `dev@loco.test` with the password `loco-dev`. The API reads its local configuration from `dev/api.env`, which holds only dev values. Exporting `GH_OAUTH_CLIENT_ID` and `GH_OAUTH_CLIENT_SECRET` for a GitHub OAuth app whose callback URL is `http://localhost:5556/dex/callback` before `mise run tilt` adds a GitHub button to the Dex login page. The CLI talks to `https://api.loco.build`, and `loco web` opens `https://loco.build`, unless told otherwise. `loco login --host` saves the host it logged in to, and later commands use it; the session belongs to that host, so switching back to production means logging in there again:

```bash
mise run build
./bin/loco login --host http://localhost:8000
./bin/loco config set webHost http://localhost:5173
cd examples/test-api/backend && ../../../bin/loco deploy backend
```

Each git worktree runs its own stack next to the main checkout's. `dev/stack.sh` gives a worktree the first free slot N from 1 to 9 and adds 100×N to the local ports, so slot 1 serves the API on `http://localhost:8100`, the web UI on `http://localhost:5273` and Dex on `http://localhost:5656/dex`. Its kind cluster is `loco-cluster-local-N`, its compose project `loco-dev-N`, and its registry and bucket aliases `loco-registry-N` and `loco-s3-N.localhost`. `mise run stack` prints the current checkout's URLs, kube context and compose project; `mise run stop` stops that stack and `mise run down` deletes its cluster, containers and volumes. The GitHub button only works on the main checkout's stack, whose Dex callback the OAuth app names.

The kind clusters run kind's default CNI. `mise run cluster:up` also starts one [cloud-provider-kind](https://github.com/kubernetes-sigs/cloud-provider-kind) container, shared by every checkout's cluster, which gives each LoadBalancer Service, such as the Gateway's Envoy Service, an address on the `kind` docker network. OrbStack routes those addresses to the host.

Builds push to a local [zot](https://zotregistry.dev) registry and upload sources to a local S3 bucket, both from `dev/compose.yaml`; the `LOCO_REGISTRY_*` and `LOCO_SOURCE_BUCKET_*` values in `dev/api.env` point the API at them. The registry uses the dev-only accounts in `dev/registry/htpasswd`: `builder` (push), `nodes` (pull) and `api` (pull, delete), each with the password `loco-dev-<name>`. `mise run cluster:registry` puts both containers on the `kind` docker network, lets the kind nodes pull from the registry under `localhost:5001` and `loco-registry:5000`, and stores the `nodes` credential as the `loco-registry` pull secret and the `builder` credential as the `loco-registry-push` secret in `loco-system`. The API names images `loco-registry:5000/...`, the address both the build pods and the kind nodes reach.

A presigned source URL signs its host, so the CLI on the host and the build pod in kind must reach the bucket under one name. The API signs URLs for `http://loco-s3.localhost:9000`. On the host, `*.localhost` resolves to loopback and reaches the published port; inside kind, the bucket's network alias `loco-s3.localhost` resolves to the container, which listens on the same port. macOS resolves `*.localhost` on its own, and so does Linux with systemd-resolved or nss-myhostname; elsewhere, add `127.0.0.1 loco-s3.localhost` to `/etc/hosts`. Build pods may not reach private addresses, so the local controller values set `builds.privateEgress` to the `kind` network's subnets on the registry and bucket ports, which `mise run cluster:build-egress` prints.

The API sends each started build to the agent of an active cluster with builds enabled over the Sync stream, the agent creates a `Build` for it, and the build controller runs each `Build` as a Job in the `loco-builds` namespace. `mise run e2e:builds` builds a fixture app end to end in a throwaway kind cluster, once by applying `Build` objects directly and once through the API, agent and controller, then deploys the result.

The `charts/loco-worker` chart installs two controller Deployments from the one `loco-controller` image: `loco-controller` runs the Application controller (`--reconciler=application`) and `loco-build-controller` runs the build controller (`--reconciler=build`), each with its own ServiceAccount and RBAC. No chart installs CRDs: `mise run crds:apply` server-side applies the `Application` and `Build` CRDs and the Gateway API CRDs from the pinned `gateway-helm` chart before any chart is installed, and Tilt and the e2e suites run it first. With `builds.enabled=false` the chart installs no build controller and no `loco-builds` namespace, and `mise run crds:apply --without-builds` leaves out the `Build` CRD; the agent then reports that its cluster does not run builds, the API refuses source builds with `no cluster in this install accepts builds`, and `loco deploy` still deploys services with `image` set. `mise run e2e:builds-disabled` checks that install end to end. A cluster created before the registry existed has to be recreated once, with `mise run cluster:down && mise run cluster:up`.

---

**Note:** This project is primarily educational, created so I can learn more about Kubernetes, networking, and security.

## License

Loco is released under the [MIT License](./LICENSE).
