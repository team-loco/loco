- users need to login (via oauth providers)

  - frontend: access tokens last 5-15 minutes, refresh tokens last 30 days. jti of refresh tokens stored in db to allow revocation.
  - personal access tokens (cli etc.):

- entityId: user:1
  org:3
  wks:1

- we will log token creation for now.
- eventually write to audit database.

- tokens themselves need to have scoped access to token creation.

- rbac

- oauth providers will be the only way to sign in - avoid duplicates by checking against email (like email provided to us by github vs email provided to us by microsoft, UNIQUE constraint on email field in users table)

- so u should only be able to create tokens with max permissions that u have.
  u cannot permission escalate.

- org:owner -- has ALL permissions
- org:create-workspaces
- org:delete-workspaces
- org:update-workspaces
- org:list-workspaces
- org:invite-user
- org:remove-user
- org:list-users
- org:give-permissions[perm1, perm2] -- by default, can always give permissions you have. the parameters are additonal permissions. it is important to note that if you can give a permission you don't have, you kinda have that permission, even if you can't grant it to yourself. you can give the permission to give permissions, but the person can only recieve permissions to give permissions that you had the power to give originally, unless someone the person can only recieve permissions to give permissions that you had the power to give originally, unless someone else gives them the power.

for example: if person 1 has "org:give-permssions[perm1, give-permissions[perm1, perm2], perm3]", they can give person 2 "give-permissions[perm1, perm2]" and can give person 2 "perm 3" but person 2 cannot give "perm 3". this also means that person 1, while not having perm2, can give person 2 the ability to give perm 2, while person 1 and person 2 don't have perm 2 for themselves. person 2 can also give person 1 "perm 2" after person 1 gives person 2 "give-permissions[perm1, perm2]", but it is impossible for person 2 to have "perm 2" for themselves. it is important to note that the "person" that im talking about aren't "users", but instead tokens. which means that the "give permissions" power means you can give permissions vested within a token.

- org:remove-permissions[perm1, perm2] -- by default, you cannot remove any permissions. the parameters are specific enumeration of permissions you can remove.

- wks:owner -- has ALL permissions, in a singular workspace.
- wks:create-projects
- wks:update-projects
- wks:delete-projects
- wks:list-projects
- wks:invite-user
- wks:remove-user
- wks:list-users
- wks:give-permissions[perm1, perm2] -- same thing as orgs but perms are only related to the wks itself.
- wks:remove-permissions[perm1, perm2] -- same thing as orgs but perms are only related to the wks.

-- current system: for now --

sys: read
sys: write
sys: admin

org: read
org: write
org: admin

wks: read
wks: write
wks: admin

project: read
project: write
project: admin
