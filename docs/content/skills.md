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

Run deployment commands from the application directory. Choose one path:

## Quick deploy

If there is no `loco.yaml`, run `loco deploy`. Loco uses the directory name, `Dockerfile`, and API defaults for the port, region, resources, and public hostname. A terminal prompts for the service name and deployment confirmation. For an agent or CI, pass `--yes` and any values that differ from the defaults:

```sh
loco deploy --name web --port 8080 --env production --yes
```

Use `--region`, `--domain`, `--dockerfile`, or `--context` when needed. `--private` omits the public hostname; `--image` deploys a public image without a source build. Check `loco deploy --help` for the installed release's flags.

## Configuration in the repository

If `loco.yaml` exists, `loco deploy` applies it. To start managing the application with a file, run `loco infra init --name <service>`, edit the generated file, then run `loco infra validate`, `loco infra plan`, and `loco deploy`. Follow the [loco.yaml reference](https://docs.loco.build/deployment/loco-yaml/) for multiple services and environment overrides.

After either path, use `loco resource status --help` to check the deployment. Report the service, environment, status, and public URL if one was assigned.
