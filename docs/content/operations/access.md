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

Without an email transport, the inviter shares the invitation link returned by the API. An installation can delegate invitation delivery to a [signed HTTP(S) hook](self-hosting.md#invitation-delivery).

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
