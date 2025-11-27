# 🚂 Loco

> Deploy containerized apps right from your terminal.

Loco is a container orchestration platform that simplifies application deployment. Run `loco deploy` and Loco handles the rest - building, deploying, and scaling your applications on Kubernetes.

## Features

- **Simple deployments** - Expose your app to the internet with just `loco deploy`!
- **Simple Configuration** - Configure all app settings with a `loco.toml` file. A sample spec with sensible defaults can be generated via `loco init`.
- **HTTPS by default** - Automatic SSL certificate management, powered by Let's Encrypt and Certificate Manager.
- **Fast Reverse Proxy** - Envoy Gateway API serves HTTP3 traffic.

## Architecture Diagram

![Architecture Diagram](./assets/arch-light.png)

## Quick Start

1.  **Download the loco cli**

```bash
go install github.com/loco-team/loco@latest
```

2. **Run `loco init` to create a `loco.toml` file.**
3. **Deploy your app via `loco deploy`**

Your app will be available at `https://myapp.deploy-app.com`

See all loco cli commands via `loco help`.
Loco also generates completions for shells such as bash and zshrc.

```bash
loco completion zsh
```

## Examples

Sample [`loco.toml`](./loco.toml)

A very simple app (currently deployed on loco) can be found here: [example-test-api](./examples/test-api/)

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

To be added later.

## Contributing

To be added later.

---

**Note:** This project is primarily educational, created so I can learn more about Kubernetes, networking, and security.
