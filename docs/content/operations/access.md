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
