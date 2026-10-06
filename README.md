# 🚂 Loco

> Deploy containerized apps right from your terminal.

Loco is a container orchestration platform that simplifies application deployment. Run `loco deploy` and Loco handles the rest - building, deploying, and scaling your applications on Kubernetes.

## Features

- **Simple deployments** - Expose your app to the internet with just `loco deploy`!
- **Simple Configuration** - Configure all app settings with a `loco.toml` file. A sample spec with sensible defaults can be generated via `loco init`.
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

2. **Log in with `loco login`.** It uses the GitHub device flow.
3. **Run `loco init` to create a `loco.toml` file**, and `loco validate` to check it.
4. **Deploy your app via `loco deploy <app-name>`**

Apps run under Kubernetes' `restricted` Pod Security profile, so the image must run as a numeric non-root user (for example `USER 10001` in the Dockerfile).

Your app will be available at `https://<app-name>.onloco.app`. `loco deploy` is shorthand for `loco resource deploy`;
`loco resource` also holds `status`, `logs`, `events`, `env`, `scale` and `destroy`.

See all loco cli commands via `loco help`.
Loco also generates completions for shells such as bash and zshrc.

```bash
loco completion zsh
```

## Examples

Every `loco.toml` field, with its default and whether it is required: [`examples/loco_example.toml`](./examples/loco_example.toml)

Deployable sample apps, each with its own `loco.toml`:

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

- **Cilium:** Implements the CNI (Container Network Interface)
- **Envoy:** Implements the 'new' Kubernetes Gateway API. Responsible for routing, TLS termination, enabling HTTP3
- **cert-manager:** For automatic SSL certificate management for various components. (Let's Encrypt).
- **OpenTelemetry:** For observability; collects metrics, logs, and will eventually collect tracing.
- **ClickHouse:** As the data store for observability data.
- **Grafana:** Dashboards for visualizing metrics and logs.

## Abuse Prevention

To avoid abuse, Loco uses an invitation system. The repo collaborators is re-purposed as an invitation list and determines who can deploy with Loco.
You must first reach out to me, nikumar1206, if you would like to deploy on this platform.

## Documentation

- **API Documentation:** [https://buf.build/team-loco/loco](https://buf.build/team-loco/loco) - Browse proto files, generate client SDKs, and view API documentation.

## Contributing

Every tool the repository uses (Go, bun, the linters, code generators, helm, kind and so on) is pinned in [`mise.toml`](./mise.toml), with exact versions and checksums for each platform in `mise.lock`. CI installs from the same files. Install [mise](https://mise.jdx.dev/getting-started.html), then from the repository root:

```bash
mise run setup
```

That installs the pinned tools, the web dependencies and the git hooks, then runs `mise run doctor`, which checks that Docker (OrbStack or Docker Desktop) and Docker Compose are running and recent enough. Docker and mise are the only tools you install yourself. With [`mise activate`](https://mise.jdx.dev/getting-started.html#activate-mise) in your shell, the pinned tools are on `PATH` whenever you are inside the repository.

Builds, tests, code generation, linting and the local environment are mise tasks, which CI and the hooks run as well. `mise tasks` lists them.

Copy `.env.example` to `.env` and fill in the GitHub OAuth app and GitLab registry credentials. `mise run tilt` then brings up the local environment, with the API on `http://localhost:8000` and the web UI on `http://localhost:5173`. The CLI talks to `https://api.loco.build`, and `loco web` opens `https://loco.build`, unless told otherwise. `loco login --host` saves the host it logged in to, and later commands use it; the session belongs to that host, so switching back to production means logging in there again:

```bash
mise run build
./bin/loco login --host http://localhost:8000
./bin/loco config set webHost http://localhost:5173
cd examples/test-api/backend && ../../../bin/loco deploy backend
```

---

**Note:** This project is primarily educational, created so I can learn more about Kubernetes, networking, and security.

## License

Loco is released under the [MIT License](./LICENSE).
