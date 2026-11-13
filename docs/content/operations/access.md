# Organizations and access

An organization holds workspaces, and a user works in a workspace only after they accept an invitation to its organization and an organization admin grants them that workspace.

## Organization roles

A user holds one or more scopes on an organization:

| Scope | Grants |
| --- | --- |
| `member` | See the organization, its member list and the workspaces the user has been granted. No access to any workspace. |
| `read` | Read every workspace in the organization. |
| `write` | Create workspaces and change every workspace in the organization. |
| `admin` | Administer every workspace in the organization, delete the organization, read its audit log, and invite and remove members. |

`read`, `write` and `admin` each include `member`. The user who creates an organization holds all three.

`OrgService.SetOrgMemberRole` lets an organization admin set an existing member to Admin (`read`, `write`, `admin`) or Member (`member` only). Workspace and resource grants stay unchanged. Demoting or removing the last organization admin fails with `FailedPrecondition`; self-demotion follows the same rule. Role changes record an `org_member.role_changed` audit event. Organization admins can also change roles from the dashboard’s Team page.

## Invite and remove members

A user joins an organization only by accepting an invitation. An organization admin invites an email address with `loco org invite <email>` (`OrgService.InviteOrgMember`), and the user becomes a `member` the next time they sign in with that verified email address. `loco org invitations` lists pending invitations and `loco org revoke-invite <id>` revokes one.

The dashboard and CLI return a sign-in link for the inviter to copy and share. Core sends no email. Installations that want invitation email consume the [`invitation.created` and `invitation.resent` webhook events](webhooks.md#invitation-events). Resending extends the invitation expiry and records a new event.

`OrgService.RemoveOrgMember` removes a member, naming the user by the ID `UserService.WhoAmI` returns for them. `OrgService.ListOrgMembers` lists each member with their organization scopes. Removing a member also removes every workspace and resource grant they hold in that organization.

## Grant a workspace

`WorkspaceService.CreateMember` grants `read`, `write` or `admin` on one workspace, and the dashboard's Team page calls it. It fails with `FailedPrecondition` for a user who is not a member of the workspace's organization; invite them to the organization first.

A member sees the workspaces they hold `read` on, plus every workspace of an organization they hold `read` on. With a user session, `loco use`, `--org` with `--workspace`, and the dashboard's workspace switcher use that set.

## Deploy from CI with an API token

Use a scoped API token to run `loco infra plan`, `loco infra apply` and `loco deploy` from a clean CI runner. Create a token on the dashboard's Tokens page and select **Read** on the target workspace for planning or **Write** for applying and deploying. The dashboard's **Write** level grants both scopes. If you create the token with `loco token create` or the API, specify both `read` and `write` for deployments so the CLI can resolve the workspace and plan before it applies.

Set `LOCO_TOKEN` from your CI secret store. Set `LOCO_HOST` to the HTTPS API URL of the instance that issued the token. Supply the organization name or ID and the workspace ID explicitly:

```sh
export LOCO_HOST=https://api.loco.build
export LOCO_ORG=my-org
export LOCO_WORKSPACE=0199b6c4-5d1e-7f00-8000-000000000001
export LOCO_ENV=production

loco infra plan
loco deploy --yes
```

Use `--org` and `--workspace` to override the corresponding environment variables, and `--env` to choose an environment by name or ID. A `--host` flag must match `LOCO_HOST` so a token cannot follow a host override. A set `LOCO_TOKEN` takes precedence over saved credentials. An empty value fails. Unset the variable to use the local session. If the API rejects an expired or revoked token, replace the CI secret with a valid token and rerun the command.

## API token lifetime

The API limits token lifetimes with `LOCO_API_TOKEN_MAX_LIFETIME`, a Go duration of at least one second, defaulting to `8760h` (365 days). `ConfigService.GetConfig` exposes the limit as `api_token_max_lifetime_seconds`. The dashboard offers expiry choices up to that limit. The dashboard and `loco token create` default to 30 days, capped at the server maximum when it is shorter. The CLI rejects explicit `--expires` durations outside the permitted range. Use `--expires 7d` to request a shorter lifetime.

## Suspend an organization

An installation's operator can stop everything an organization runs without deleting any of it, for abuse, non-payment or a legal hold. `OrgService.SuspendOrg` takes the organization ID and a reason, and `OrgService.ResumeOrg` lifts the suspension. Both require `system:admin`; organization admins cannot call them.

```sh
curl -sS "$LOCO_API/loco.org.v1.OrgService/SuspendOrg" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"orgId":"<org-id>","reason":"Unpaid invoice"}'
```

While an organization is suspended:

- Every application of its workspaces is removed from its cluster, so none of its pods run. Services, specs, secrets, domains and deployment history stay as they are.
- Its queued and running builds are canceled.
- Deployments, applies that deploy, scaling and new builds fail with `FailedPrecondition` and the reason, so `loco deploy` fails too.
- Reads, secret and domain changes, deletes and member changes keep working.
- The dashboard shows the reason in a banner on the organization's pages, and `OrgService.GetOrg` returns it in `suspended_at` and `suspension_reason`.

`ResumeOrg` sends each active deployment's application back to its cluster, including secret and domain changes made during the suspension. Both calls are idempotent: suspending a suspended organization keeps the first reason and records nothing, and resuming an active one changes nothing. Each change records an `org.suspended` or `org.resumed` event, which the organization's audit log lists and install webhooks can subscribe to.

## Count limits

The API caps how many of each thing can exist, so one account cannot fill the control plane. A create past a cap fails with `ResourceExhausted` and a message that names the count, the limit and the variable that raises it, such as `the workspace has 100 of 100 resources; delete one or ask a Loco administrator to raise LOCO_LIMIT_RESOURCES_PER_WORKSPACE`. `loco` prints that message and exits non-zero, and a `loco infra apply` or `loco deploy` that would create services or domains past a cap changes nothing. Lowering a limit deletes nothing; it only refuses new creates.

| Limit | Counted per | Default | API variable |
| --- | --- | --- | --- |
| Organizations | user who created them | 10 | `LOCO_LIMIT_ORGS_PER_USER` |
| Workspaces | organization | 100 | `LOCO_LIMIT_WORKSPACES_PER_ORG` |
| Members | organization | 500 | `LOCO_LIMIT_MEMBERS_PER_ORG` |
| Open invitations | organization | 100 | `LOCO_LIMIT_INVITATIONS_PER_ORG` |
| Environments | workspace | 10 | `LOCO_LIMIT_ENVIRONMENTS_PER_WORKSPACE` |
| Resources | workspace | 100 | `LOCO_LIMIT_RESOURCES_PER_WORKSPACE` |
| Webhooks | workspace | 20 | `LOCO_LIMIT_WEBHOOKS_PER_WORKSPACE` |
| API tokens | user, organization, workspace or resource that owns them | 50 | `LOCO_LIMIT_TOKENS_PER_ENTITY` |
| Domains | resource in one environment | 10 | `LOCO_LIMIT_DOMAINS_PER_RESOURCE` |
| Secrets | service in one environment | 256 | `LOCO_SECRETS_MAX_PER_SERVICE` |

An invitation is refused while the organization is at its member or open-invitation limit. A user signing in with an open invitation to an organization at its member limit signs in without joining it, and the invitation stays open until it expires.

Deployments and builds are not capped; the API deletes the oldest instead. Creating a deployment deletes the resource's deployments in that environment past the newest `LOCO_LIMIT_DEPLOYMENT_HISTORY` (100), except active and unfinished deployments and those a cluster still runs. Creating a build deletes the resource's builds past the newest `LOCO_LIMIT_BUILD_HISTORY` (100) once the image and source sweepers have deleted their image and source; with `LOCO_IMAGE_RETENTION=0` succeeded builds are kept. Their events stay in the audit log until `EVENTS_RETENTION` removes them.

`ConfigService.GetConfig` reports the install defaults in `count_limits`. A system admin can override every limit except organizations per user for one organization, for example to give a large customer more workspaces. Per-workspace and per-resource overrides apply to every workspace or resource of the organization. Each change needs a reason of 1 to 500 characters and records an `org.limits_changed` event with the reason and the old and new values:

```sh
locoadm org limits set <org-id> --workspaces-per-org 500 --reason "Enterprise contract"
locoadm org limits unset <org-id> workspaces-per-org --reason "Contract ended"
locoadm org limits get <org-id>
```

`OrgService.GetOrgLimits` returns an organization's effective limits, the ones it overrides and the latest reason to any member, and the dashboard shows the usage beside each create button, such as "42 of 100 workspaces". `OrgService.SetOrgLimits` takes the limits, an `update_mask` naming the ones to change, where 0 returns a limit to the install default, and the reason; it requires `system:admin`.
