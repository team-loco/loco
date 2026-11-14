<p align="center">
  <img src="web/public/logo.svg" alt="Loco" width="240">
</p>

<p align="center"><strong>Deploy from your terminal. Run on Kubernetes.</strong></p>

<p align="center">
Loco is an open-source, self-hostable platform that builds, deploys and scales containerized applications on Kubernetes. It is released under the MIT license, and the whole platform is in this repository.
</p>

<p align="center">
  <a href="https://docs.loco.build/">Docs</a> ·
  <a href="#quick-start">Install</a> ·
  <a href="#self-hosting">Self-host</a> ·
  <a href="#contributing">Contributing</a>
</p>

## About

You deploy with the `loco` CLI from an application directory, then use the dashboard to inspect what is running. Loco turns each service into Kubernetes workloads, routing, certificates and telemetry, so a team ships containers without writing Kubernetes manifests. It suits application teams that want a deploy workflow on their own clusters, and operators who run that platform for them.

Loco runs as multi-tenant SaaS, as a dedicated installation or self-hosted. See [deployment modes](https://docs.loco.build/deployment/modes/) for who operates each one.

## Features

- Deploy from a `Dockerfile` with `loco deploy`, or deploy a public image with `--image`. See [install](https://docs.loco.build/getting-started/install/) and [connect](https://docs.loco.build/getting-started/connect/).
- Keep services, regions, sizing and domains in a [`loco.yaml`](https://docs.loco.build/deployment/loco-yaml/) and preview changes with `loco infra plan`.
- Separate staging and production with [environments](https://docs.loco.build/deployment/environments/) that have their own configuration and secrets.
- Give public services a hostname under a platform domain, with HTTPS routing. Private services accept traffic only from inside their workspace.
- Manage per-service secrets with `loco env`; no command prints a value.
- Scale replicas, CPU and memory with `loco resource scale`, or autoscale on CPU or memory.
- Read logs, metrics and traces in the dashboard and the CLI. See [application operations](https://docs.loco.build/operations/applications/).
- Control access with organizations, workspaces and roles, and deploy from CI with scoped API tokens. See [organizations and access](https://docs.loco.build/operations/access/).
- Send platform events to your own HTTP endpoints with [webhooks](https://docs.loco.build/operations/webhooks/).

## Quick start

Install the CLI on Linux or macOS (amd64 or arm64), sign in, and deploy the application in the current directory. The directory needs a `Dockerfile`.

```sh
curl -fsSL https://loco.build/install.sh | sh
loco login
loco deploy --name web --port 8080 --yes
```

Run `loco help` to list the commands your installed release supports.

## Self-hosting

Self-hosting puts the control plane and the worker clusters under your team's control. The repository holds every platform component and the Helm charts for the control plane and worker clusters. The [self-hosting guide](https://docs.loco.build/operations/self-hosting/) lists the supported Kubernetes versions, installation inputs and upgrade steps.

## Documentation

- [Documentation](https://docs.loco.build/)
- [Architecture](https://docs.loco.build/reference/architecture/)
- [`loco.yaml` reference](https://docs.loco.build/deployment/loco-yaml/)

### For agents

- [`AGENTS.md`](AGENTS.md): repository layout, commands, and rules for contributors
- [`skills.md`](https://loco.build/skills.md): instructions for deploying an application to Loco
- [`llms.txt`](https://docs.loco.build/llms.txt) and [`llms-full.txt`](https://docs.loco.build/llms-full.txt): the documentation as Markdown

## Contributing

Issues and pull requests are welcome. Install [mise](https://mise.jdx.dev/getting-started.html), then run `mise run setup` to install the pinned tools, web dependencies and git hooks. `mise tasks` lists the build, test and lint tasks. [Local development](https://docs.loco.build/contributing/development/) covers running the platform on your machine.

## License

Loco is released under the [MIT License](LICENSE).
