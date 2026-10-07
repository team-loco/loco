# Environments

An environment separates an application's staging and production targets. Select the API instance first, then the workspace and environment within that instance.

## Loco's hosted endpoints

| Surface | Production | Staging |
| --- | --- | --- |
| Dashboard | `https://loco.build` | `https://staging.loco.build` |
| API | `https://api.loco.build` | `https://api.staging.loco.build` |
| Documentation (canonical) | `https://docs.loco.build` | `https://docs.staging.loco.build` |
| Documentation on the dashboard | `https://loco.build/docs/` | `https://staging.loco.build/docs/` |

The staging docs display a banner and tell search engines not to index them. These endpoints describe Loco's own hosted services; self-hosted installations choose their own domains.

## Application environments in Go definitions

!!! warning "Unreleased workflow"

    Workspace and environment selection here follows the [Go infrastructure cutover](go-infrastructure.md), which requires a CLI release containing that workflow.

```sh
loco infra context --workspace WORKSPACE_ID --environment ENVIRONMENT_ID
```

A definition receives the workspace name, environment name, and environment type (`dev`, `staging`, or `production`). Use that context to choose capacity and domains. Staging and production can share service keys while keeping separate resources and deployment history.

Explicit `--workspace` and `--environment` flags override environment variables and a saved link. Prefer IDs for restricted CI credentials.

## Promote the dashboard and documentation

The UI image includes its documentation build. Repository merges build production and staging UI images for the same commit. The existing staging workflow deploys that commit automatically. The production workflow promotes the selected commit's production UI image, including the docs, after staging verification. See [documentation maintenance](../contributing/documentation.md).
