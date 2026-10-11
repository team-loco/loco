# CLI and API

Loco's command help and protobuf definitions are the reference for the installed release's interface. Use the CLI's own help to see current arguments and defaults:

```sh
loco help
loco resource --help
loco config --help
```

## CLI commands

| Command | Purpose |
| --- | --- |
| `loco login`, `loco logout`, `loco whoami` | Sign in to a Loco instance, sign out, show the signed-in user |
| `loco org`, `loco workspace`, `loco use` | Manage organizations and workspaces and choose the current one |
| `loco infra init`, `validate`, `plan`, `apply`, `transfer` | Work with [`loco.yaml`](../deployment/loco-yaml.md) |
| `loco deploy` | Build and deploy the services of `loco.yaml` |
| `loco resource` | Inspect and manage applications |
| `loco builds` | List, inspect, follow and cancel builds |
| `loco token` | Manage API tokens |
| `loco web` | Open the configured dashboard |
| `loco config` | Manage local CLI settings |
| `loco update` | Update the installed CLI |
| `loco completion` | Generate shell completions |

## API schema

Browse [Loco on the Buf Schema Registry](https://buf.build/team-loco/loco) for service definitions and generated client options. The repository's `proto/` directory owns the schema.

```sh
mise run gen
```

That task regenerates Go and TypeScript clients and SQL query bindings. Do not hand-edit generated files.
