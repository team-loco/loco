# Go infrastructure

A deployment definition is a standalone Go module in `.loco/`. Loco compiles the module, supplies JSON context on stdin, and reads a versioned JSON manifest from stdout. The API receives declarative data; it never executes the definition. Helpers, loops and environment conditions run locally.

## Author a stack

```go
package main

import loco "github.com/team-loco/loco/sdk/go"

func main() {
    loco.Run(func(ctx *loco.Context) *loco.Stack {
        replicas := int32(1)
        if ctx.EnvironmentType == "production" {
            replicas = 2
        }
        return &loco.Stack{
            Name: "storefront",
            Services: []*loco.Service{{
                Key: "api",
                Source: loco.Docker(".", "Dockerfile"),
                Hostname: "storefront-" + ctx.Environment + ".onloco.app",
                Spec: loco.Config(&loco.ServiceSpec{
                    Routing: &loco.Routing{Port: 8000},
                    Regions: map[string]*loco.Region{
                        "us-east-1": {
                            Enabled: true, Primary: true,
                            Cpu: "100m", Memory: "256Mi",
                            MinReplicas: replicas, MaxReplicas: replicas,
                        },
                    },
                    HealthCheck: &loco.Health{Path: "/health"},
                    Observability: &loco.Observability{
                        Logging: &loco.Logging{Enabled: loco.Value(false)},
                        Metrics: &loco.Metrics{Enabled: true},
                    },
                }),
                Variables: map[string]*loco.Variable{
                    "LOG_LEVEL": loco.Literal("info"),
                    "DATABASE_URL": loco.SecretRef("database-url"),
                },
            }},
        }
    })
}
```

`loco init` writes the entrypoint and module. Install its pinned SDK dependency with `cd .loco && go mod tidy`; commit `go.mod` and `go.sum`. Compilation uses `-mod=readonly`. The SDK aliases generated protobuf messages from the standalone `gen/go` module. Publish `gen/go/v0.1.0` before `sdk/go/v0.1.0`; the SDK depends on that generated module rather than the CLI/API root module. Examples use a local replace directive for repository development.

A service declares exactly one Docker build or image source. Docker paths resolve relative to the application root and cannot escape it, including through symlinks. A missing domain keeps the service private. Routing ports are 1024–65535. Each region declares CPU, memory and replica bounds; current server limits still apply. Pointer helpers such as `loco.Value(false)` preserve explicit optional values. Application defaults come exclusively from the API’s existing service defaults; the SDK and offline validation leave omitted settings unset.

## SDK surface

All configuration types, including CPU/memory resources, autoscaling, logging, metrics and tracing, are aliases of shared protobuf messages. There are no handwritten copies of their fields and no field-by-field conversions between SDK and API types. Proto3 optional fields preserve omitted versus explicit false/zero values.

The public functions are authoring helpers: `Run`, `Docker`, `Image`, `Config`, `Literal`, `SecretRef`, `Preserve` and `Value`. They construct or emit the graph. Evaluation, JSON decoding and validation utilities are internal to the CLI/API; the SDK does not expose `Evaluate`, `Decode`, `Normalize`, or state-management operations. This follows the authoring-helper scope of [Railway’s Go SDK](https://github.com/railwayapp/railway-go-sdk/blob/main/railway.go). Loco’s existing resource and observability settings remain supported.

CI runs `buf generate`, compiles the generated module and SDK, and rejects modified or untracked generated output. SDK aliases inherit schema additions directly; compilation checks field references throughout the clients and server.

## Context and target selection

Authenticated commands resolve `--workspace` and `--environment`, falling back to `LOCO_WORKSPACE` and `LOCO_ENVIRONMENT`. A selected CLI workspace can supply the workspace default. An environment is required. `loco infra link --workspace WORKSPACE_ID --environment ENVIRONMENT_ID --stack storefront` saves a local default in `.loco/link.json`; explicit flags and environment variables take precedence. Ignore the link file in Git. IDs work with restricted CI credentials; name lookup requires access to the parent workspace.

The API supplies workspace name, environment name and environment type (`dev`, `staging`, `production`). Loco supplies the absolute application root. The definition cannot select or change its API target by returning a different context.

```sh
loco infra context --workspace WORKSPACE_ID --environment ENVIRONMENT_ID
loco validate --workspace my-team --environment production --environment-type production --json
```

`validate` is offline: its context flags are explicit authoring inputs and do not authorize a deployment. Evaluated definitions do not inherit Loco tokens, registry credentials or arbitrary application environment variables. Run definitions only in a machine/container appropriate for executing the repository’s code; environment filtering is not an execution sandbox.

## Plans and deployment

```sh
loco infra plan --workspace my-team --environment staging --out plan.json
loco deploy --workspace my-team --environment staging --plan-only --out release.json
loco infra apply --plan release.json --yes --wait
```

`infra plan` previews infrastructure without building Docker images. `deploy` builds and publishes images, then plans infrastructure and releases together. Image sources used for releases must be pinned with `@sha256:…`; build output is resolved to a registry digest.

Saved plans expire after 24 hours. Apply sends the immutable plan ID and fingerprints; it does not reevaluate Go or rebuild images. Source changes, target changes and environment intent changes reject the saved plan. Status observations do not invalidate plans. Apply retries return the existing apply/deployment IDs.

One transaction writes desired infrastructure and deployment intent. Kubernetes reconciliation happens afterward; `--wait` observes the resulting deployments. A failed rollout does not undo already committed desired infrastructure.

Services have stable keys within a stack. Every `(environment, stack, service key)` has its own resource instance. Staging and production can use identical service names while retaining separate domains, resources, placements and deployment history. Full-stack apply removes services absent from the owned stack; selecting `deploy api` or `infra plan --service api` preserves sibling services. Destructive operations require `--confirm-destructive` and admin permission. Fields declared by the stack, including its complete environment-variable map, are owned by the definition; dashboard edits produce drift for the next plan.

## Secrets

```sh
printf '%s' "$DATABASE_URL" | loco secret set database-url --workspace WORKSPACE_ID --environment ENVIRONMENT_ID
loco infra pull --workspace WORKSPACE_ID --environment ENVIRONMENT_ID --stack storefront
```

Secret versions, stored plans, service variable values and placement environment values are encrypted with `INFRA_ENCRYPTION_KEY`, a base64-encoded 32-byte key shared across API replicas. Plans show variable changes without values. `pull` writes editable Go and exports literal variables as `Preserve()` references; named secret references keep their names. Values persist before a first deployment. The dashboard shows keys and accepts replacements without reading existing values; it cannot export secret values. Preserve requires a current value and therefore cannot initialize another environment. Secret references resolve to an immutable value when planning; changing a secret invalidates uncommitted plans.

## CI credentials

Create an environment credential with explicit scopes and an optional stack restriction:

```sh
loco token create storefront-release --entity-type environment --entity-id ENVIRONMENT_ID --stack storefront --scope read,write,admin --expires 7d
```

Use `read` for planning, `read,write` for publishing and non-destructive apply, and add `admin` when reviewed deletions are allowed. Supply the token as `LOCO_TOKEN` only to the steps that need it. Stack-restricted credentials cannot access sibling stacks, change the environment or change shared secrets. Registry publishing uses the API’s scoped proxy; project-wide GitLab credentials stay on the API server.

## Git review and merge

Use separate runners for uncredentialed execution and credentialed publishing/apply. The following sequence is intended for a maintained workflow on the default branch; fork PRs must not receive deployment secrets. Install a pinned, checksum-verified released CLI independently of application source. Do not build the CLI or invoke application-provided scripts in a credentialed job.

1. Resolve public context for the target environment using a trusted job or repository variables.
2. Check out the PR head with persisted Git credentials disabled. Evaluate Go and build application images in a runner that has no deployment credentials.
3. Pass the normalized manifest and image archives to a fresh publishing runner. Check out the exact same source tree, publish the archives, and save the immutable plan. Display the redacted plan in the review.
4. After merge, download the plan associated with the reviewed PR head. Check out the merged tree and apply that exact plan. Serialize applies for each environment. A changed tree, stale environment revision or expired plan requires a new review.

```sh
loco validate --workspace "$WORKSPACE_NAME" --environment "$ENVIRONMENT_NAME" --environment-type "$ENVIRONMENT_TYPE" --json > "$RUNNER_TEMP/manifest.json"
loco infra build --manifest "$RUNNER_TEMP/manifest.json" --export-dir "$RUNNER_TEMP/loco-build" --reviewed
```

The publishing runner receives the build directory and original `manifest.json`:

```sh
loco infra publish --artifact "$RUNNER_TEMP/loco-build/build.json" --out "$RUNNER_TEMP/images.json" --workspace "$WORKSPACE_ID" --environment "$ENVIRONMENT_ID" --stack storefront
loco infra plan --manifest "$RUNNER_TEMP/manifest.json" --images "$RUNNER_TEMP/images.json" --reviewed --workspace "$WORKSPACE_ID" --environment "$ENVIRONMENT_ID" --out "$RUNNER_TEMP/plan.json"
```

The merge runner receives the saved `plan.json`:

```sh
loco infra apply --plan "$RUNNER_TEMP/plan.json" --yes --confirm-destructive --wait
```

Source fingerprints cover the repository’s tracked tree, executable bits and file contents, rather than the commit ID; a merge/squash that produces the same reviewed tree can apply the plan. Put generated artifacts outside the checkout. Reviewed builds use a snapshot of tracked files; ignored or untracked files cannot enter the application image. External local module replacements, symlinks, submodules and unresolved LFS pointers are rejected. Review both application and infrastructure changes: application-only changes must run this workflow too. Protect the workflow, deployment environment and plan artifacts with the repository’s review rules.

The reusable GitHub workflow example is [git-go-infrastructure.yml](./examples/git-go-infrastructure.yml). It requires a published CLI containing these commands and an available Go SDK version before activation.
