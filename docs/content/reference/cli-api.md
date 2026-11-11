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
| `locoadm validate --file installation.yaml` | Validate the unreleased [installation file](installation.md) contract |
| `locoadm plan`, `locoadm apply`, `locoadm status` | Plan, install and inspect the installation selected by `--file` |
| `locoadm bootstrap --file installation.yaml --email admin@example.com` | Create the first administrator through the installation's identity provider and save its CLI session |
| `locoadm cluster create`, `update`, `list` | Register clusters, set application/build capabilities and inspect [cluster attachment](cluster-attachment.md) |
| `locoadm verify` | Build disposable source and verify secret delivery, HTTPS and logs |
| `locoadm recovery export` | Encrypt seal material and individual recovery shares for separate custodians |
| `loco login`, `loco logout`, `loco whoami` | Sign in to a Loco instance, sign out, show the signed-in user |
| `loco org`, `loco workspace`, `loco use` | Manage organizations and workspaces and choose the current one |
| `loco infra init`, `validate`, `plan`, `apply`, `transfer` | Work with [`loco.yaml`](../deployment/loco-yaml.md) |
| `loco deploy` | Deploy from the current directory with API defaults, or build and deploy the services of `loco.yaml` |
| `loco resource` | Inspect and manage applications |
| `loco builds` | List, inspect, follow and cancel builds |
| `loco env` | Set, remove and list a service's secrets in an environment |
| `loco token` | Manage API tokens |
| `loco web` | Open the configured dashboard |
| `loco config` | Manage local CLI settings |
| `loco update` | Update the installed CLI |
| `loco completion` | Generate shell completions |

Workspace updates change only supplied flags. To clear a description, pass an empty value:

```sh
loco workspace update <workspace-id> --description ""
```

## API schema

Browse [Loco on the Buf Schema Registry](https://buf.build/team-loco/loco) for service definitions and generated client options. The repository's `proto/` directory owns the schema.

```sh
mise run gen
```

That task regenerates Go and TypeScript clients, the OpenAPI 3.1 schema, and SQL query bindings. Do not hand-edit generated files.

The API serves its embedded schema at `GET /openapi.json` without authentication. Its `info.version` reports the running API binary's build version; the generated file in the repository contains an `(unbuilt)` placeholder. The schema describes the Connect RPC routes, including streaming methods and the proxied observability service. Streaming methods use Connect framing and require a compatible client.

Open `/` on the API host to browse the schema and try unary RPCs with Scalar. The page includes searchable navigation, request examples, and light and dark themes. It loads Scalar from jsDelivr; the API embeds only the HTML page and schema. Requests go directly to the API. Browsing needs no authentication; RPC calls retain their authentication requirements.

```sh
curl --fail https://api.loco.build/openapi.json -o openapi.json
```

Settings update requests use optional fields. Omitted fields and JSON `null` preserve the stored value. An explicit empty description clears it; an empty profile name or avatar URL clears that field. Organization, workspace, environment and application names must remain nonempty. An explicitly unspecified environment type is invalid.

`ClusterAdminService.CreateCluster` requires a UUID `request_id`. Persist it before sending and reuse it to recover a lost response. A retry returns the same cluster ID; changed name, region or tier, or a removed cluster, fails. Pending enrollment gets a new join token, invalidating the previous token. An enrolled cluster returns no join token and keeps its agent identity. `locoadm cluster create` generates a fresh request ID for each invocation. Administrative cluster reads include operator-owned `applications_enabled` and `builds_allowed`, plus `builds_enabled`, the readiness reported by the agent. Create accepts both operator capabilities; registration retries preserve their current values. `ClusterAdminService.UpdateCluster` replaces both capabilities without changing existing workloads. `locoadm cluster update` requires explicit `--applications-enabled` and `--builds-allowed` values.
