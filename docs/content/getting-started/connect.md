---
title: "Connect to Loco"
---

A CLI session belongs to a Loco API host. Sign in to the instance that operates your applications:

```sh
loco login
```

The default API host is `https://api.loco.build`; the default dashboard is `https://loco.build`. Follow the sign-in instructions printed by the installed CLI. For noninteractive deployments, configure a [scoped API token in CI](../operations/access.md#deploy-from-ci-with-an-api-token).

An account that belongs to no organization gets one on its first sign-in from the CLI or the dashboard, named after the account (`Ada's Org`, or `ada@example.com's Org` without a profile name), with a `default` workspace.

An account signs in through the identity that created it: the same identity-provider account each time. If sign-in fails, check that you selected the correct identity-provider account and verified its email address, or contact the instance administrator.

Create API tokens with a saved login or a token in `LOCO_TOKEN` that has the required permissions. Organization, workspace and resource tokens require `admin` on their owner; personal tokens require `user:write` on the caller's own account. Requested scopes cannot exceed the caller's current permissions or the owner's hierarchy.

## Staging

```sh
loco login --host https://api.staging.loco.build
loco config set webHost https://staging.loco.build
loco web
```

The CLI saves the host used for login. Later commands use that host. Sign in again when switching instances; a staging session does not sign you into production.

## Dedicated and self-hosted instances

Use `loco login --host` with the API URL supplied by the instance operator, and set `webHost` to that instance's dashboard URL. [Deployment modes](../deployment/modes.md) explains who operates each instance.

## Return to production

```sh
loco login --host https://api.loco.build
loco config set webHost https://loco.build
```

For commands supported by your installed release, run `loco help`. Run `loco deploy` from an application directory to deploy with API defaults, without writing a configuration file:

```sh
loco deploy
loco deploy --name web --port 8080 --yes
```

The CLI uses the default region and resources. Without `--port` the service runs as a worker and takes no traffic; with `--port` it also gets a hostname under a platform domain. In a terminal it asks for the service name; without a terminal it uses the directory name unless `--name` is set. Use `--private` to omit a public hostname, or `--image` to deploy a public image. To keep service configuration in the repository, write a [`loco.yaml`](../deployment/loco-yaml.md).
