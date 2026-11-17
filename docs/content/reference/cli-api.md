---
title: "CLI and API"
---

Loco's command help and protobuf definitions are the reference for the installed release's interface. Use the CLI's own help to see current arguments and defaults:

```sh
loco help
loco resource --help
loco config --help
```

## CLI commands

| Command | Purpose |
| --- | --- |
| `locoadm init` | Scaffold an operator GitOps repository with plaintext bootstrap Secrets |
| `locoadm status --host API_URL` | Read clusters, enrollment, heartbeats, DNS targets, version skew and unclaimed admins from the API |
| `locoadm login` | Sign in with explicit `--api` and `--web` URLs with the same session as `loco login`; a configured admin's first sign-in makes it a system admin |
| `locoadm admin list`, `grant <user-id>`, `revoke <user-id>` | List, grant and revoke [system admins](../operations/self-hosting.md#first-system-admin) |
| `locoadm verify` | Build disposable source and verify secret delivery, HTTPS and logs |
| `locoadm recovery export --context CONTEXT --namespace NAMESPACE` | Encrypt seal material and individual recovery shares for separate custodians |
| `locoadm org limits get`, `set`, `unset` | Show or override an organization's [count limits](../operations/access.md#count-limits) |
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

`ClusterAdminService.CreateCluster` requires a UUID `request_id`. Persist it before sending and reuse it to recover a lost response. A retry returns the same cluster ID; changed name, region or tier, or a removed cluster, fails. Pending enrollment gets a new join token, invalidating the previous token. An enrolled cluster returns no join token and keeps its agent identity. `locoadm cluster create` generates a fresh request ID for each invocation. Administrative cluster reads include `role`, which the agent reports from its install: `apps`, `builds` or `all`.

`ClusterAdminService.CreateCluster` and `IssueJoinToken` accept an optional positive `ttl` duration, bounded by `LOCO_CLUSTER_JOIN_TOKEN_MAX_TTL` (default `168h`). Omission uses `LOCO_CLUSTER_JOIN_TOKEN_TTL` (default `1h`).

## Operator repository

`locoadm init` writes the [GitOps example](https://github.com/team-loco/loco/tree/main/examples/gitops) to a new directory. Supply public API and dashboard URLs, an application domain, region, administrator email, a digest-pinned bundle, and an API environment file:

```sh
locoadm init --output ./fleet \
  --api https://api.example.com --web https://app.example.com \
  --domain apps.example.com --region example-east --admin admin@example.com \
  --bundle oci://ghcr.io/team-loco/loco/platform@sha256:BUNDLE_DIGEST \
  --api-env-file ./api.env
```

`--single-cluster --name local` creates one `clusters/local/` directory using the bundle’s `./all` entry point, with values for both releases. Without `--single-cluster`, the repository contains a control plane and a staging worker.

The command generates the static seal key and writes it and the supplied API environment as plaintext Kubernetes Secret files. Encrypt these files before committing, or deliver the named Secrets through External Secrets. The generated README describes optional encryption with operator-installed SOPS. Review chart values, provide infrastructure credentials and bootstrap Flux separately. Apply the worker join-token Secret directly with kubectl; the agent waits for the mounted token.

`locoadm cluster create` and `cluster join-token` accept `--ttl` and `-o secret` to print a plaintext Kubernetes Secret manifest. Default output is JSON. `--namespace` defaults to `loco-system`; `--secret-name` defaults to `loco-agent-join-token`. Redirect or pipe the manifest to your own secret-delivery tooling.

```sh
locoadm cluster create --name worker-staging --region example-east --tier staging \
  -o secret | kubectl --context worker-staging apply -f -
```

`status` requires a system-admin session. It reports `pending` or `enrolled` for each cluster and compares agent and API versions: the API supports its own minor version and the preceding minor; agents ahead of the API are flagged. Development versions report `unknown`.
