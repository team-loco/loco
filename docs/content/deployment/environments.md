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

## Application environments in loco.yaml

Plan, apply and deploy act on one environment of a workspace. Choose it with a flag or an environment variable:

```sh
loco deploy --workspace my-team --env staging
LOCO_WORKSPACE=my-team LOCO_ENV=production loco infra plan
```

`--workspace` overrides `LOCO_WORKSPACE`, which overrides the workspace saved by `loco use`. `--env` overrides `LOCO_ENV`. Without either, a workspace with a single environment uses it, and a workspace with several asks in a terminal and fails otherwise.

The same `loco.yaml` serves every environment. A service's `environments` block overrides its fields for one environment, or turns the service off there with `enabled: false`; see [loco.yaml](loco-yaml.md#environment-overrides). Staging and production keep separate resources and deployment history.

## Promote the dashboard and documentation

The UI image includes its documentation build. Repository merges build production and staging UI images for the same commit. The existing staging workflow deploys that commit automatically. The production workflow promotes the selected commit's production UI image, including the docs, after staging verification. See [documentation maintenance](../contributing/documentation.md).
