# Organizations and access

An organization holds workspaces, and a user works in a workspace only after an organization admin makes them a member of its organization and grants them that workspace.

## Organization roles

A user holds one or more scopes on an organization:

| Scope | Grants |
| --- | --- |
| `member` | See the organization, its member list and the workspaces the user has been granted. No access to any workspace. |
| `read` | Read every workspace in the organization. |
| `write` | Create workspaces and change every workspace in the organization. |
| `admin` | Administer every workspace in the organization, delete the organization, read its audit log, and add and remove members. |

`read`, `write` and `admin` each include `member`. The user who creates an organization holds all three.

## Add and remove members

An organization admin adds a user with `OrgService.AddOrgMember` and removes one with `OrgService.RemoveOrgMember`, naming the user by the ID `UserService.WhoAmI` returns for them. `OrgService.ListOrgMembers` lists each member with their organization scopes. Removing a member also removes every workspace and resource grant they hold in that organization.

## Grant a workspace

`WorkspaceService.CreateMember` grants `read`, `write` or `admin` on one workspace, and the dashboard's Team page calls it. It fails with `FailedPrecondition` for a user who is not a member of the workspace's organization; add them to the organization first.

A member sees the workspaces they hold `read` on, plus every workspace of an organization they hold `read` on. `loco use`, `--org` with `--workspace`, and the dashboard's workspace switcher all list that set.

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
