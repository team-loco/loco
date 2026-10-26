# Connect to Loco

A CLI session belongs to a Loco API host. Sign in to the instance that operates your applications:

```sh
loco login
```

The default API host is `https://api.loco.build`; the default dashboard is `https://loco.build`. Follow the sign-in instructions printed by the installed CLI.

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

The CLI uses the default region, resources and a hostname under a platform domain. In a terminal it asks for the service name; without a terminal it uses the directory name unless `--name` is set. Use `--private` to omit a public hostname, or `--image` to deploy a public image. To keep service configuration in the repository, write a [`loco.yaml`](../deployment/loco-yaml.md).
