---
name: loco-deploy
description: Deploy an application to Loco's managed multi-tenant platform with loco.yaml and the Loco CLI. Use when a user asks to deploy, update, or prepare an application for Loco.
---

# Deploy an application to Loco

Loco builds and deploys applications from `loco.yaml` to its managed Kubernetes platform. Work in the user's application repository. Read the [deployment reference](https://docs.loco.build/deployment/loco-yaml/) when editing the file, and check `loco help` for the installed CLI's commands.

## Inspect the application

Find the application entry point, Dockerfile, build context, listening port, health endpoint, environment variables, and external dependencies. Check whether the repo already has `loco.yaml`; preserve its services and environment overrides. An image must run as a non-root user and listen on an unprivileged port. See [container requirements](https://docs.loco.build/operations/applications/#container-requirements).

## Connect to Loco

If `loco` is missing, follow the [CLI installation guide](https://docs.loco.build/getting-started/install/). Sign in to the managed platform with `loco login`. Authentication may require the user to finish a browser flow. Use `loco whoami` to confirm the active account. Identify the intended organization, workspace, and environment with the user or the existing project configuration; do not guess a production target. Use `loco org --help`, `loco workspace --help`, and `loco use --help` for the installed CLI's selection commands.

## Prepare loco.yaml

If the file does not exist, run `loco infra init --name <application-name>` and review its output before editing it. The API supplies the default region, resources, port, and available platform domain. Match the service's `dockerfile`, `context`, `port`, and optional `health` to the application. A service without `domains` is private. Use only a hostname under a platform domain served by every target cluster. Do not invent a region or hostname; check the generated file and the [loco.yaml reference](https://docs.loco.build/deployment/loco-yaml/).

Keep secret values out of `loco.yaml`. List required names under the service's `secrets` field. Obtain values from the user or their approved secret source, then use `loco env push --service <service> --env <environment> <private-env-file>` to set them. Keep that file out of version control and remove it when it is no longer needed. Never print secret values in the final report.

## Validate and deploy

Run these commands from the directory containing `loco.yaml`, using the selected environment:

```sh
loco infra validate
loco infra plan --env <environment>
loco deploy --env <environment>
```

Review the plan before deploying. A partial owns its services: removing a service from its file plans its deletion. If the plan imports or deletes services, show the exact changes to the user before using the corresponding confirmation flags. A new service with missing declared secrets may be created without a build; set the secrets and rerun `loco deploy` as instructed by the CLI. Do not put secrets on a command line that will be shared in logs or chat.

After deployment, inspect `loco resource status --help` and use the supported status command to verify the service and its regions. If the application is public, test its reported URL and health endpoint. If it is private, verify its status and logs. Report the deployed workspace, environment, service, observed status, and URL when one exists. Include any unresolved deployment error.
