# Application operations

The CLI reads application status and manages application resources through the selected Loco API. Check command help for the arguments supported by your release:

```sh
loco resource --help
loco resource status --help
loco resource logs --help
loco resource events --help
```

`loco resource` also includes `env`, `scale`, and `destroy`. Inspect an operation's help before changing resources or removing an application.

## Secrets

`loco env` manages the secrets of one service in one environment. `--service` names the service and is required; `--env` or `LOCO_ENV` selects the environment. Secrets belong to that service only, so two services that need the same value each get it set. `push` sends every `KEY=VALUE` line of a `.env` file (or stdin) in one call, `set` takes assignments on the command line, `unset` removes names and `list` shows names, versions and who set them. No command prints a value, and `$NAME` in a `.env` file stays literal. `unset` refuses a name that the service's active deployment declares in `loco.yaml`; remove it there and apply first.

```sh
loco env push --service web --env production .env
loco env set --service web --env production API_KEY=abc123
loco env unset --service web --env production API_KEY
loco env list --service web --env production
```

## Container requirements

Applications run under Kubernetes' `restricted` Pod Security profile. Images must run as a numeric non-root user, such as `USER 10001`, and use an unprivileged application port. Configure the routing port to match the port the application listens on.

## Public and private applications

An application with a domain is public: the gateway routes its hostname to the application. An application without a domain is private: it runs, scales and writes logs like any other, makes outbound connections, and accepts connections only from inside its workspace. To deploy a private application, leave `domains` off the service in `loco.yaml`:

```yaml
services:
  worker:
    image: ghcr.io/acme/worker:1.4.2
    regions:
      us-east-1: { cpu: 100m, memory: 256Mi, replicas: { min: 1, max: 1 } }
```

`loco deploy` then prints that the application has no public URL, and `loco resource status` shows `URL: none`. The dashboard's new-service form offers the same choice under Networking. A domain added to an existing application takes effect on its next deployment, and removing an application's last domain makes it private from its next deployment.

## Investigate a rollout

Use application status to find whether a deployment is progressing or ready. Read events for scheduling and reconciliation failures, then inspect logs for application startup failures. The dashboard provides another view of the same applications.

```sh
loco web
```

To define services and their domains, see [loco.yaml](../deployment/loco-yaml.md).
