---
title: "Environments"
---

An environment separates an application's configuration, secrets and deployment history within a workspace. Each new workspace starts with an environment named `production`. Add environments with names such as `staging` or `preview` in workspace settings; names do not select a cluster tier. An environment name is 1 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit. Select the API instance first, then the workspace and environment within that instance.

## Loco's hosted endpoints

| Surface | Production | Staging |
| --- | --- | --- |
| Dashboard | `https://loco.build` | `https://staging.loco.build` |
| API | `https://api.loco.build` | `https://api.staging.loco.build` |
| Documentation (canonical) | `https://docs.loco.build` | `https://docs.staging.loco.build` |
| Documentation on the dashboard | `https://loco.build/docs/` | `https://staging.loco.build/docs/` |

The staging docs display a banner and tell search engines not to index them. These endpoints describe Loco's own hosted services; self-hosted installations choose their own domains.

## Application environments in loco.yaml

Plan, apply, deploy and `loco resource scale` act on one environment of a workspace. Choose it with a flag or an environment variable:

```sh
loco deploy --workspace my-team --env staging
LOCO_WORKSPACE=my-team LOCO_ENV=production loco infra plan
```

With a user session, `--workspace` overrides `LOCO_WORKSPACE`, which overrides the workspace saved by `loco use`. [API tokens](../operations/access.md#deploy-from-ci-with-an-api-token) require an explicit workspace ID. `--env` overrides `LOCO_ENV` and both accept an environment name or ID. Without either, a workspace with a single environment uses it, and a workspace with several asks in a terminal and fails otherwise.

The same `loco.yaml` serves every environment. A service's `environments` block overrides its fields for one environment, or turns the service off there with `enabled: false`; see [loco.yaml](loco-yaml.md#environment-overrides). Staging and production share resource identities and keep separate runtime configuration, workloads and deployment history.

Source builds started by `loco deploy` record the selected environment. The dashboard shows those builds and their activity in that environment. A build's image remains reusable in other environments; a deployment can still open its source build and logs there. Builds created through the API without an origin appear only in unfiltered build history, including `loco builds list`.

A service accepts changes at any time, including while a deployment is building or rolling out. A change that creates a deployment, such as scaling, a deploy or a configuration change, replaces the deployment in flight in the same region: that deployment ends `canceled` and the version that was serving before it keeps serving until the new one is ready. A restart during a rollout rolls the replicas again.

Saving secrets starts no rollout; new values take effect on the next restart or deploy. Setting a secret to its existing value is a no-op.

## Promote the dashboard and documentation

The UI image includes its documentation build. Repository merges build production and staging UI images for the same commit. The existing staging workflow deploys that commit automatically. The production workflow promotes the selected commit's production UI image, including the docs, after staging verification. See [documentation maintenance](../contributing/documentation.md).
