# Go infrastructure

A Go infrastructure definition describes an application stack in a standalone `.loco/` module. Loco evaluates the definition locally and sends declarative data to the API.

!!! warning "Unreleased workflow"

    This page describes the Go infrastructure cutover under review in [PR #316](https://github.com/team-loco/loco/pull/316). It requires a released CLI containing those commands and published Go SDK dependencies. Check `loco infra --help` before using this workflow.

## Create a definition

```sh
loco init
cd .loco
go mod tidy
```

The Go workflow writes `.loco/main.go` and `.loco/go.mod`. Commit the definition, `go.mod`, and `go.sum`. Definitions compile with `-mod=readonly`.

The SDK entrypoint is `loco.Run`, imported from `github.com/team-loco/loco/sdk/go`. A stack contains services with stable keys. Each service declares one Docker build or image source, resource settings, and environment variables. Omit a hostname to keep a service private.

See the [source definition and SDK example](https://github.com/team-loco/loco/blob/iac/14-git-workflow-docs/docs/go-infrastructure.md) for the exact authoring API under review.

## Select a target

```sh
loco infra link --workspace WORKSPACE_ID --environment ENVIRONMENT_ID --stack storefront
loco infra context --workspace WORKSPACE_ID --environment ENVIRONMENT_ID
```

`infra link` saves a local default in `.loco/link.json`; ignore that file in Git. Explicit flags and `LOCO_WORKSPACE` / `LOCO_ENVIRONMENT` override the link. An environment is required.

## Validate, plan, and apply

```sh
loco validate --workspace my-team --environment staging --environment-type staging --json
loco deploy --workspace my-team --environment staging --plan-only --out release.json
loco infra apply --plan release.json --yes --wait
```

`validate` runs offline. `deploy --plan-only` builds and publishes images, then saves a plan covering infrastructure and releases. Review the plan before applying it. `infra apply` uses the saved plan; it does not reevaluate the definition or rebuild images.

A full-stack apply removes services absent from the owned stack. Deletions require admin permission and `--confirm-destructive`. Applying desired state and rolling out Kubernetes workloads are separate steps; a failed rollout does not undo committed desired state.

## Variables and secrets

Use `loco.Literal` for declared values, `loco.SecretRef` for named secrets, and `loco.Preserve` to retain an existing value. Preserve cannot initialize a new environment.

```sh
printf '%s' "$DATABASE_URL" | loco secret set database-url --workspace WORKSPACE_ID --environment ENVIRONMENT_ID
```

Stack definitions own their declared variable maps. Dashboard edits create drift for the next plan. Supply secrets separately for each environment.
