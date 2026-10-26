---
name: loco-deploy
description: Deploy an application to Loco's multi-tenant platform with the CLI. Use when a user asks to deploy or update an application on Loco.
---

# Deploy to Loco

Install the CLI on Linux or macOS (amd64 or arm64), then sign in:

```sh
curl -fsSL https://loco.build/install.sh | sh
loco login
```

Inspect the project before deploying. Decide whether it has one service or several. Run deployment commands from the project directory.

## Quick deploy

For one service without `loco.yaml`, run `loco deploy`. Loco turns the directory name into a safe service name and assigns a hostname under a platform domain reported by the API, such as `web-fytr.onloco.app`. It uses `Dockerfile` and API defaults for the port, region, and resources. A terminal prompts for the service name and deployment confirmation. For an agent or CI, pass `--yes` and any values that differ from the defaults:

```sh
loco deploy --name web --port 8080 --env production --yes
```

Use `--region`, `--domain`, `--dockerfile`, or `--context` when needed. `--private` omits the public hostname; `--image` deploys a public image without a source build. Check `loco deploy --help` for the installed release's flags.

## Configuration in the repository

For multiple services, create `loco.yaml` even when none exists. Run `loco infra init --name <service>` to create a starter file, then define each service under `services` with its own `context`, `dockerfile` or `image`, `port`, and regions as needed. The hosted JSON Schema is [https://loco.build/schemas/loco.v1.json](https://loco.build/schemas/loco.v1.json); `loco infra init` writes its URL into the file for editor validation. Follow the [loco.yaml reference](https://docs.loco.build/deployment/loco-yaml/) for the fields and environment overrides. Run `loco infra validate`, `loco infra plan`, and `loco deploy`. If `loco.yaml` already exists, update it and deploy from the directory that holds it.

After either path, run `loco resource status <service>` to check the deployment. A service is one kind of Loco resource; `resource` is the current CLI command for status. Report the service, environment, status, and public URL if one was assigned.
