# Application operations

The CLI reads application status and manages application resources through the selected Loco API. Check command help for the arguments supported by your release:

```sh
loco resource --help
loco resource status --help
loco resource logs --help
loco resource events --help
```

`loco resource` also includes `env`, `scale`, and `destroy`. Inspect an operation's help before changing resources or removing an application.

## Container requirements

Applications run under Kubernetes' `restricted` Pod Security profile. Images must run as a numeric non-root user, such as `USER 10001`, and use an unprivileged application port. Configure the routing port to match the port the application listens on.

## Investigate a rollout

Use application status to find whether a deployment is progressing or ready. Read events for scheduling and reconciliation failures, then inspect logs for application startup failures. The dashboard provides another view of the same applications.

```sh
loco web
```

For deployment authoring, see [Go infrastructure](../deployment/go-infrastructure.md) and its release notice.
