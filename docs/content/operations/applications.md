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

`loco env` manages the secrets of an environment, selected with `--env` or `LOCO_ENV`. `push` sends every `KEY=VALUE` line of a `.env` file (or stdin) in one call, `set` takes assignments on the command line, `unset` removes names and `list` shows names, versions and who set them. No command prints a value, and `$NAME` in a `.env` file stays literal.

```sh
loco env push --env production .env
loco env set --env production API_KEY=abc123
loco env unset --env production API_KEY
loco env list --env production
```

## Container requirements

Applications run under Kubernetes' `restricted` Pod Security profile. Images must run as a numeric non-root user, such as `USER 10001`, and use an unprivileged application port. Configure the routing port to match the port the application listens on.

## Public and private applications

An application with a domain is public: the gateway routes its hostname to the application. An application without a domain is private: it runs, scales and writes logs like any other, makes outbound connections, and accepts connections only from inside its workspace. To deploy a private application, leave `[DomainConfig]` out of `loco.toml`:

```toml
[Routing]
Port = 8000
```

`loco deploy` then prints that the application has no public URL, and `loco resource status` shows `URL: none`. The dashboard's new-service form offers the same choice under Networking. A domain added to an existing application takes effect on its next deployment, and removing an application's last domain makes it private from its next deployment.

## Investigate a rollout

Use application status to find whether a deployment is progressing or ready. Read events for scheduling and reconciliation failures, then inspect logs for application startup failures. The dashboard provides another view of the same applications.

```sh
loco web
```

For deployment authoring, see [Go infrastructure](../deployment/go-infrastructure.md) and its release notice.
