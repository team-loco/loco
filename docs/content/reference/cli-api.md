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
| `loco login` | Sign in to a Loco instance |
| `loco web` | Open the configured dashboard |
| `loco resource` | Inspect and manage applications |
| `loco config` | Manage local CLI settings |
| `loco update` | Update the installed CLI |
| `loco completion` | Generate shell completions |

The [Go infrastructure](../deployment/go-infrastructure.md) page covers the unreleased `infra` workflow separately.

## API schema

Browse [Loco on the Buf Schema Registry](https://buf.build/team-loco/loco) for service definitions and generated client options. The repository's `proto/` directory owns the schema.

```sh
mise run gen
```

That task regenerates Go and TypeScript clients and SQL query bindings. Do not hand-edit generated files.
