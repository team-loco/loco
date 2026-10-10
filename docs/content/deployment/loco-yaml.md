# loco.yaml

`loco.yaml` declares the services of a workspace: what to run, where, and how much. The CLI sends the file to the API, which plans the difference against an environment and applies it.

## Create a file

```sh
loco infra init --name storefront
```

`init` writes a starter `loco.yaml` from the API's defaults: the port, CPU, memory and replica counts, the default region, and a hostname under the platform domain. The platform domain is the one set with `loco config set defaultAppDomain`, or else the one the API reports. When neither exists, `init` fails. The first line of the file points editors at the JSON Schema the API serves (`schemas/loco.v1.json` in the repository), which gives completion and inline errors.

```yaml
version: 1
partial: storefront

services:
  web:
    dockerfile: Dockerfile
    port: 8080
    health: { path: /healthz }
    domains: [storefront.example.com]
    env: { LOG_LEVEL: info }
    regions:
      us-east-1: { cpu: 250m, memory: 512Mi, replicas: { min: 1, max: 3 } }
  worker:
    image: ghcr.io/acme/worker:1.4.2
    regions:
      us-east-1: { cpu: 100m, memory: 256Mi, replicas: { min: 1, max: 1 } }
```

A service builds from source or runs a public `image`, never both. A source service builds in its `context`, a directory relative to `loco.yaml` that defaults to `.`, from its `dockerfile`, a path relative to `context` that defaults to `Dockerfile`. This service builds from `services/web/deploy/Dockerfile` with `services/web` as the build context:

```yaml
services:
  web:
    context: services/web
    dockerfile: deploy/Dockerfile
```

A service without `domains` is private. Each region needs `cpu`, `memory`, `replicas.min` and `replicas.max`; `autoscaling` takes exactly one of `cpuTarget` or `memoryTarget`. Service and partial names are DNS labels. The API checks resource limits when it plans the file.

Check the structure without calling the API:

```sh
loco infra validate
```

## Environment overrides

`environments.<name>` changes a service for one environment. Objects merge key by key, lists and scalars replace the whole value, and `null` removes a key. `enabled: false` leaves the service out of that environment.

```yaml
services:
  web:
    dockerfile: Dockerfile
    port: 8080
    domains: [staging.storefront.example.com]
    regions:
      us-east-1: { cpu: 250m, memory: 512Mi, replicas: { min: 1, max: 1 } }
    environments:
      production:
        domains: [www.storefront.example.com]
        regions:
          us-east-1: { replicas: { min: 2, max: 3 } }
  debug-proxy:
    image: ghcr.io/acme/debug-proxy:1
    regions:
      us-east-1: { cpu: 100m, memory: 128Mi, replicas: { min: 1, max: 1 } }
    environments:
      production:
        enabled: false
```

Commands pick the environment with `--env` or `LOCO_ENV`; a workspace with one environment needs neither, and with several the CLI asks when it runs in a terminal. Select the workspace with `--workspace` or `LOCO_WORKSPACE`, and the organization with `--org` or `LOCO_ORG`.

## Partials

The `partial` key names the slice of the workspace that the file owns. A service belongs to one partial, so several repositories can each manage their own services in one workspace.

- A service in the file that belongs to another partial is a plan error.
- A service in the partial that the file no longer lists is deleted. `services: {}` deletes every service of the partial; a file without a `services` key is invalid.
- A running service that belongs to no partial and appears in the file is imported into the partial.

Move a service to another partial, so that the file declaring that partial owns it from then on:

```sh
loco infra transfer api --to platform
```

## Plan, apply and deploy

```sh
loco infra plan --env staging
loco infra apply --env staging
loco deploy --env staging
```

`plan` prints the operations an apply would perform (create, update, delete, import) and changes nothing. With `--detailed-exit-code` it exits 2 when there are changes, and `--json` prints the plan as JSON. `apply` shows the plan and asks for confirmation; it fails if the environment changed since the plan was shown. Pass `--yes` when there is no terminal. Delete operations need `--confirm-destructive`, and imports need `--confirm-import`.

`loco deploy` applies the file and builds its source services. It packs the directory that holds `loco.yaml` once, leaving out `.git`, `.env`, `.env.*` and anything the `.dockerignore` in that directory excludes except the Dockerfiles it builds, uploads it, and builds each service with its own `dockerfile` and `context`. A source service the environment does not have yet is created first so it can be built; every other change in the plan is applied with the builds, so a failed build changes nothing else. Image services deploy without a build. Name services to build only those:

```sh
loco deploy web --env production --yes
```

## Variables and secrets

`env` holds plain values, which appear in the plan and in the file.

!!! note "Secrets are not available yet"
    `secrets` lists secret names the service reads, and the file never contains their values. Loco has no command to store a secret value yet, so the plan reports every listed secret as missing in the environment. Leave `secrets` out until secret storage ships.
