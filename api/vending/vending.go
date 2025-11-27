package vending

import (
	"context"
	"os"

	"github.com/golang-jwt/jwt/v5"
	"github.com/loco-team/loco/api/db"
)

const issuer = "loco-api"
const (
	ScopeSysRead      = "sys:read"      // system-level read access (e.g., list all orgs, list users, list clusters, read system "event trail", etc.: infra visibility)
	ScopeSysWrite     = "sys:write"     // system-level write access (e.g., create clusters, delete clusters, run migrations, undo migrations, etc.: infra changes)
	ScopeSysAdmin     = "sys:admin"     // system-level admin access (e.g., permission giving at the system level, spin up loco db)
	ScopeOrgRead      = "org:read"      // organization-level read access (e.g., list workspaces, list users in org, see billing info, see org "event trail", etc.: org visibility)
	ScopeOrgWrite     = "org:write"     // organization-level write access (e.g., create workspaces, delete workspaces, invite users to org, remove users from org, etc.: org changes)
	ScopeOrgAdmin     = "org:admin"     // organization-level admin access  (e.g org update billing, perm giving at org level, etc.: org admin tasks)
	ScopeWksRead      = "wks:read"      // workspace-level read access (e.g., view workspace resources, view workspace settings, read workspace "event trail", etc.: workspace visibility)
	ScopeWksWrite     = "wks:write"     // workspace-level write access (e.g., create resources in workspace, update workspace settings, add users, remove users, etc.: workspace changes)
	ScopeWksAdmin     = "wks:admin"     // workspace-level admin access (e.g., delete workspace, perm giving at workspace level, etc.: workspace admin tasks)
	ScopeProjectRead  = "project:read"  // project-level read access (e.g., view project resources, view project settings, view project "event trail", etc.: project visibility)
	ScopeProjectWrite = "project:write" // project-level write access (e.g., create resources in project, update project settings, add users, remove users, etc.: project changes)
	ScopeProjectAdmin = "project:admin" // project-level admin access (e.g., delete project, perm giving at project level, etc.: project admin tasks)
	ScopeUserRead     = "user:read"     // user-level read access (e.g., view own data, view what orgs that u are a member of, view user "event trail", view what wks u are a member of, own settings, etc.: personal visibility)
	ScopeUserWrite    = "user:write"    // user-level write access (e.g., create orgs, update own profile, update own settings, leave orgs, leave wks, etc.: personal changes)
	ScopeUserAdmin    = "user:admin"    // user-level admin access (e.g., delete own account, etc.: personal admin tasks)
)
const (
	ActionListOrgs             = "list_orgs"               // list all organizations in the system
	ActionListUsers            = "list_users"              // list all users in the system
	ActionListClusters         = "list_clusters"           // list all clusters in the system
	ActionReadSystemEventTrail = "read_system_event_trail" // read the system-level event trail
	ActionReadSystemSettings   = "view_system_settings"    // view system-wide settings

	ActionCreateUser          = "create_user"          // create a new user in the system
	ActionDeleteUser          = "delete_user"          // delete a user from the system
	ActionCreateSystemCluster = "create_cluster"       // create a new cluster in the system
	ActionDeleteSystemCluster = "delete_cluster"       // delete an existing cluster from the system
	ActionSystemMigrations    = "run_migrations"       // run migrations on the system database
	ActionEditSystemSettings  = "edit_system_settings" // edit system-wide settings

	ActionEditSystemPermissions = "system_edit_permissions" // edit permissions (system-level, grant/revoke sys:read, sys:write, sys:admin, but also org-level r/w/a, wks-level, and project-level r/w/a)
	ActionSystemDatabase        = "system_database"         // create/delete/edit the loco system database

	// org
	ActionListWorkspaces    = "list_workspaces"      // list all workspaces in an organization
	ActionListOrgUsers      = "list_org_users"       // list all users in an organization
	ActionReadOrgBilling    = "view_org_billing"     // view billing information for an organization
	ActionReadOrgEventTrail = "read_org_event_trail" // read the organization-level event trail

	ActionCreateWorkspace = "create_workspace"  // create a new workspace in an organization
	ActionDeleteWorkspace = "delete_workspace"  // delete an existing workspace from an organization
	ActionInviteOrgUser   = "invite_org_user"   // invite a user to join an organization
	ActionRemoveOrgUser   = "remove_org_user"   // remove a user from an organization
	ActionEditOrgSettings = "edit_org_settings" // edit settings for an organization

	ActionEditOrgBilling     = "edit_org_billing"     // edit billing information for an organization
	ActionEditOrgPermissions = "edit_org_permissions" // edit permissions (org-level r/w/a, but also wks-level and project-level r/w/a)
	ActionDeleteOrg          = "delete_org"           // delete the organization

	// workspace
	ActionListProjects      = "list_projects"        // list all projects in a workspace
	ActionReadWksSettings   = "read_wks_settings"    // view settings for a workspace
	ActionReadWksEventTrail = "read_wks_event_trail" // read the workspace-level event trail

	ActionCreateProject   = "create_project"    // create a new project in a workspace
	ActionDeleteProject   = "delete_project"    // delete an existing project from a workspace
	ActionAddWksUser      = "add_wks_user"      // add a user to a workspace
	ActionRemoveWksUser   = "remove_wks_user"   // remove a user from a workspace
	ActionEditWksSettings = "edit_wks_settings" // edit settings for a workspace

	ActionEditWksPermissions = "edit_wks_permissions" // edit permissions (wks-level r/w/a, but also project-level r/w/a)

	// project
	ActionReadProjectSettings   = "read_project_settings"    // view settings for a project
	ActionReadProjectEventTrail = "read_project_event_trail" // read the project-level event trail

	ActionCreateProjectResource = "create_project_resource" // create a new resource in a project
	ActionDeleteProjectResource = "delete_project_resource" // delete an existing resource from a project
	ActionProjectDeploy         = "deploy_project"          // deploy a resource
	ActionProjectUndeploy       = "undeploy_project"        // undeploy a resource
	ActionAddProjectUser        = "add_project_user"        // add a user to a project
	ActionRemoveProjectUser     = "remove_project_user"     // remove a user from a project
	ActionEditProjectSettings   = "edit_project_settings"   // edit settings for a project

	ActionEditProjectPermissions = "edit_project_permissions" // edit permissions (project-level r/w/a)

	// user
	ActionReadOwnData        = "read_own_data"         // view own data
	ActionReadOwnOrgs        = "read_own_orgs"         // view what organizations the user is a member of
	ActionReadOwnWks         = "read_own_workspaces"   // view what workspaces the user is a member of
	ActionReadUserEventTrail = "read_user_event_trail" // read the user-level event trail

	ActionCreateOrg         = "create_org"          // create a new organization
	ActionUpdateOwnProfile  = "update_own_profile"  // update own profile
	ActionUpdateOwnSettings = "update_own_settings" // update own settings
	ActionLeaveOrg          = "leave_org"           // leave an organization
	ActionLeaveWks          = "leave_workspace"     // leave a workspace

	ActionDeleteOwnAccount = "delete_own_account" // delete own account
)

// the actions that map to scopes that can perform the action
// the list is an OR list - having any one of the scopes allows the action to be completed
// in the future, if an AND list is needed, it could be a list of lists
var actionScopes = map[string][]string{
	ActionListOrgs:             {ScopeSysRead},
	ActionListUsers:            {ScopeSysRead},
	ActionListClusters:         {ScopeSysRead},
	ActionReadSystemEventTrail: {ScopeSysRead},

	ActionCreateUser:          {ScopeSysWrite},
	ActionDeleteUser:          {ScopeSysWrite},
	ActionCreateSystemCluster: {ScopeSysWrite},
	ActionDeleteSystemCluster: {ScopeSysWrite},
	ActionSystemMigrations:    {ScopeSysWrite},

	ActionEditSystemPermissions: {ScopeSysAdmin},
	ActionSystemDatabase:        {ScopeSysAdmin},

	// org
	ActionListWorkspaces:    {ScopeOrgRead},
	ActionListOrgUsers:      {ScopeOrgRead},
	ActionReadOrgBilling:    {ScopeOrgRead},
	ActionReadOrgEventTrail: {ScopeOrgRead},

	ActionCreateWorkspace: {ScopeOrgWrite},
	ActionDeleteWorkspace: {ScopeOrgWrite, ScopeWksAdmin},
	ActionInviteOrgUser:   {ScopeOrgWrite},
	ActionRemoveOrgUser:   {ScopeOrgWrite},
	ActionEditOrgSettings: {ScopeOrgWrite},

	ActionEditOrgBilling:     {ScopeOrgAdmin},
	ActionEditOrgPermissions: {ScopeOrgAdmin},
	ActionDeleteOrg:          {ScopeOrgAdmin, ScopeSysWrite},

	// workspace
	ActionListProjects:      {ScopeWksRead},
	ActionReadWksSettings:   {ScopeWksRead},
	ActionReadWksEventTrail: {ScopeWksRead},

	ActionCreateProject:   {ScopeWksWrite},
	ActionDeleteProject:   {ScopeWksWrite, ScopeProjectAdmin},
	ActionAddWksUser:      {ScopeWksWrite},
	ActionRemoveWksUser:   {ScopeWksWrite},
	ActionEditWksSettings: {ScopeWksWrite},

	ActionEditWksPermissions: {ScopeWksAdmin},
	// the workspace admin can also delete the workspace, as listed above

	// project
	ActionReadProjectSettings:   {ScopeProjectRead},
	ActionReadProjectEventTrail: {ScopeProjectRead},

	ActionCreateProjectResource: {ScopeProjectWrite},
	ActionDeleteProjectResource: {ScopeProjectWrite},
	ActionProjectDeploy:         {ScopeProjectWrite},
	ActionProjectUndeploy:       {ScopeProjectWrite},
	ActionAddProjectUser:        {ScopeProjectWrite},
	ActionRemoveProjectUser:     {ScopeProjectWrite},
	ActionEditProjectSettings:   {ScopeProjectWrite},

	ActionEditProjectPermissions: {ScopeProjectAdmin},
	// the project admin can also delete the project, as listed above

	// user
	ActionReadOwnData:        {ScopeUserRead},
	ActionReadOwnOrgs:        {ScopeUserRead},
	ActionReadOwnWks:         {ScopeUserRead},
	ActionReadUserEventTrail: {ScopeUserRead},

	ActionCreateOrg:         {ScopeUserWrite},
	ActionUpdateOwnProfile:  {ScopeUserWrite},
	ActionUpdateOwnSettings: {ScopeUserWrite},
	ActionLeaveOrg:          {ScopeUserWrite},
	ActionLeaveWks:          {ScopeUserWrite},

	ActionDeleteOwnAccount: {ScopeUserAdmin},
}

// something like org read doesn't let you read workspaces in the org unless you also have workspace read
// the creator of an entity (org, wks, project) automatically gets admin on that entity
// admin scope on an entity allows you to grant yourself the read/write perms on that entity but also read/write/admin on child entities
// admin scope on an entity does NOT allow you to grant yourself admin on parent entities
// meaning an org admin can give themselves wks read/write/admin and a wks admin can give themselves project read/write/admin

// event trails are logs of events being performed on the entity
// system trail: org creation, org deletion, permission changes, cluster creation/deletion, db deployment, token created for "on behalf of" the system, etc.
// org trail: workspace creation, workspace deletion user invites/removals, permission changes, billing changes, token created for "on behalf of" the org, etc.
// workspace trail: project creation, resource creation/deletion, user invites/removals, permission changes, token created for "on behalf of" the workspace, etc.
// project trail: resource creation/deletion, user invites/removals, permission changes, token created for "on behalf of" the project, etc.
// user trail: own account changes, org joins/leaves, wks joins/leaves, logins etc, token created for "on behalf of" the user, etc.

type Claims struct {
	EntityID string   `json:"entity_id"` // on behalf of which entity the token is issued
	Scopes   []string `json:"scopes"`    // scopes associated with the token
	jwt.RegisteredClaims
}

type TokenService interface {
	IssueToken(ctx context.Context, subject string, scopes []string, opts ...IssueOption) (string, error)
	ValidateToken(ctx context.Context, token string) (*Claims, error)
	RefreshToken(ctx context.Context, refreshToken string) (string, string, error)
	RevokeToken(ctx context.Context, tokenID string) error
}

type VendingMachine struct {
	db     *db.DB
	secret []byte
}

func (tvm *VendingMachine) Issue(scopes []string) (string, error) {

}

func NewVendingMachine(db *db.DB) *VendingMachine {
	secret := os.Getenv("JWT_SECRET")
	if len(secret) == 0 {
		panic("JWT_SECRET not set")
	}
	return &VendingMachine{
		db:     db,
		secret: []byte(secret),
	}
}
