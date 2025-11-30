package tvm

import (
	"errors"

	queries "github.com/loco-team/loco/api/gen/db"
)

const issuer = "loco-api"

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
	ActionListApps          = "list_apps"            // list all projects in a workspace
	ActionReadWksSettings   = "read_wks_settings"    // view settings for a workspace
	ActionReadWksEventTrail = "read_wks_event_trail" // read the workspace-level event trail

	ActionCreateApp       = "create_app"        // create a new project in a workspace
	ActionDeleteApp       = "delete_app"        // delete an existing project from a workspace
	ActionAddWksUser      = "add_wks_user"      // add a user to a workspace
	ActionRemoveWksUser   = "remove_wks_user"   // remove a user from a workspace
	ActionEditWksSettings = "edit_wks_settings" // edit settings for a workspace

	ActionEditWksPermissions = "edit_wks_permissions" // edit permissions (wks-level r/w/a, but also project-level r/w/a)

	// project
	ActionReadAppSettings   = "read_app_settings"    // view settings for a project
	ActionReadAppEventTrail = "read_app_event_trail" // read the project-level event trail

	ActionCreateAppResource = "create_app_resource" // create a new resource in a project
	ActionDeleteAppResource = "delete_app_resource" // delete an existing resource from a project
	ActionAppDeploy         = "deploy_app"          // deploy a resource
	ActionAppUndeploy       = "undeploy_app"        // undeploy a resource
	ActionAddAppUser        = "add_app_user"        // add a user to a project
	ActionRemoveAppUser     = "remove_app_user"     // remove a user from a project
	ActionEditAppSettings   = "edit_app_settings"   // edit settings for a project

	ActionEditAppPermissions = "edit_app_permissions" // edit permissions (project-level r/w/a)

	// user
	ActionReadUserInfo       = "read_user_info"        // view user info
	ActionReadUserOrgs       = "read_user_orgs"        // view what organizations the user is a member of
	ActionReadUserWks        = "read_user_workspaces"  // view what workspaces the user is a member of
	ActionReadUserEventTrail = "read_user_event_trail" // read the user-level event trail
	ActionReadUserSettings   = "read_user_settings"    // read user settings

	ActionEditUserInfo     = "edit_user_info"     // edit user info
	ActionEditUserSettings = "edit_user_settings" // edit user settings
	ActionCreateOrg        = "create_org"         // create a new organization
	ActionLeaveOrg         = "leave_org"          // leave an organization
	ActionLeaveWks         = "leave_workspace"    // leave a workspace

	ActionDeleteOwnAccount = "delete_own_account" // delete own account
)

func Action(entity queries.Entity, action string) []queries.EntityScope {
	scopes, ok := actionScopes[action]
	if !ok {
		return nil
	}

	entityScopes := make([]queries.EntityScope, 0, len(scopes))
	for _, scope := range scopes {
		entityScopes = append(entityScopes, scope.attachEntityID(entity.ID))
	}

	return entityScopes
}

// entityTypeScope is a helper struct to represent a combination of entity type and scope.
// e.g. org:read, wks:write, app:admin, etc. while not remaining specific to any specific entity ID.
// this isn't represented in the db directly, but is useful for mapping actions to required scopes.
type entityTypeScope struct {
	EntityType queries.EntityType
	Scope      queries.Scope
}

// attachEntityID attaches a specific entity ID to the entityTypeScope, returning a full EntityScope.
// this is used when checking if a user has the required scopes for a given action on a specific entity.
// since having org:read in one org doesn't let u read in another org.
func (e entityTypeScope) attachEntityID(id int64) queries.EntityScope {
	return queries.EntityScope{
		Entity: queries.Entity{
			Type: e.EntityType,
			ID:   id,
		},
		Scope: e.Scope,
	}
}

var scopeSysRead = entityTypeScope{EntityType: queries.EntityTypeSystem, Scope: queries.ScopeRead}
var scopeSysWrite = entityTypeScope{EntityType: queries.EntityTypeSystem, Scope: queries.ScopeWrite}
var scopeSysAdmin = entityTypeScope{EntityType: queries.EntityTypeSystem, Scope: queries.ScopeAdmin}

var scopeOrgRead = entityTypeScope{EntityType: queries.EntityTypeOrganization, Scope: queries.ScopeRead}
var scopeOrgWrite = entityTypeScope{EntityType: queries.EntityTypeOrganization, Scope: queries.ScopeWrite}
var scopeOrgAdmin = entityTypeScope{EntityType: queries.EntityTypeOrganization, Scope: queries.ScopeAdmin}

var scopeWksRead = entityTypeScope{EntityType: queries.EntityTypeWorkspace, Scope: queries.ScopeRead}
var scopeWksWrite = entityTypeScope{EntityType: queries.EntityTypeWorkspace, Scope: queries.ScopeWrite}
var scopeWksAdmin = entityTypeScope{EntityType: queries.EntityTypeWorkspace, Scope: queries.ScopeAdmin}

var scopeAppRead = entityTypeScope{EntityType: queries.EntityTypeApp, Scope: queries.ScopeRead}
var scopeAppWrite = entityTypeScope{EntityType: queries.EntityTypeApp, Scope: queries.ScopeWrite}
var scopeAppAdmin = entityTypeScope{EntityType: queries.EntityTypeApp, Scope: queries.ScopeAdmin}

var scopeUserRead = entityTypeScope{EntityType: queries.EntityTypeUser, Scope: queries.ScopeRead}
var scopeUserWrite = entityTypeScope{EntityType: queries.EntityTypeUser, Scope: queries.ScopeWrite}
var scopeUserAdmin = entityTypeScope{EntityType: queries.EntityTypeUser, Scope: queries.ScopeAdmin}

var actionScopes = map[string][]entityTypeScope{
	ActionListOrgs:             {scopeSysRead},
	ActionListUsers:            {scopeSysRead},
	ActionListClusters:         {scopeSysRead},
	ActionReadSystemEventTrail: {scopeSysRead},
	ActionReadSystemSettings:   {scopeSysRead},

	ActionCreateUser:          {scopeSysWrite},
	ActionDeleteUser:          {scopeSysWrite},
	ActionCreateSystemCluster: {scopeSysWrite},
	ActionDeleteSystemCluster: {scopeSysWrite},
	ActionSystemMigrations:    {scopeSysWrite},
	ActionEditSystemSettings:  {scopeSysWrite},

	ActionEditSystemPermissions: {scopeSysAdmin},
	ActionSystemDatabase:        {scopeSysAdmin},

	// org
	ActionListWorkspaces:    {scopeOrgRead},
	ActionListOrgUsers:      {scopeOrgRead},
	ActionReadOrgBilling:    {scopeOrgRead},
	ActionReadOrgEventTrail: {scopeOrgRead},

	ActionCreateWorkspace: {scopeOrgWrite},
	ActionDeleteWorkspace: {scopeOrgWrite, scopeWksAdmin},
	ActionInviteOrgUser:   {scopeOrgWrite},
	ActionRemoveOrgUser:   {scopeOrgWrite},
	ActionEditOrgSettings: {scopeOrgWrite},

	ActionEditOrgBilling:     {scopeOrgAdmin},
	ActionEditOrgPermissions: {scopeOrgAdmin},
	ActionDeleteOrg:          {scopeOrgAdmin, scopeSysWrite},

	// workspace
	ActionListApps:          {scopeWksRead},
	ActionReadWksSettings:   {scopeWksRead},
	ActionReadWksEventTrail: {scopeWksRead},

	ActionCreateApp:       {scopeWksWrite},
	ActionDeleteApp:       {scopeWksWrite, scopeAppAdmin},
	ActionAddWksUser:      {scopeWksWrite},
	ActionRemoveWksUser:   {scopeWksWrite},
	ActionEditWksSettings: {scopeWksWrite},

	ActionEditWksPermissions: {scopeWksAdmin},

	// project
	ActionReadAppSettings:   {scopeAppRead},
	ActionReadAppEventTrail: {scopeAppRead},

	ActionCreateAppResource: {scopeAppWrite},
	ActionDeleteAppResource: {scopeAppWrite},
	ActionAppDeploy:         {scopeAppWrite},
	ActionAppUndeploy:       {scopeAppWrite},
	ActionAddAppUser:        {scopeAppWrite},
	ActionRemoveAppUser:     {scopeAppWrite},
	ActionEditAppSettings:   {scopeAppWrite},

	ActionEditAppPermissions: {scopeAppAdmin},

	// user
	ActionReadUserInfo:       {scopeUserRead},
	ActionReadUserOrgs:       {scopeUserRead},
	ActionReadUserWks:        {scopeUserRead},
	ActionReadUserEventTrail: {scopeUserRead},
	ActionReadUserSettings:   {scopeUserRead},

	ActionEditUserInfo:     {scopeUserWrite},
	ActionEditUserSettings: {scopeUserWrite},
	ActionCreateOrg:        {scopeUserWrite},
	ActionLeaveOrg:         {scopeUserWrite},
	ActionLeaveWks:         {scopeUserWrite},

	ActionDeleteOwnAccount: {scopeUserAdmin},
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

var (
	ErrDurationExceedsMaxAllowed = errors.New("token duration exceeds maximum allowed")
	ErrInsufficentPermissions    = errors.New("insufficient permissions")
	ErrStoreToken                = errors.New("unable to store issued token")

	ErrTokenExpired   = errors.New("token has expired")
	ErrTokenNotFound  = errors.New("token not found")
	ErrGithubExchange = errors.New("an issue occured while exchanging the github token")
	ErrUserNotFound   = errors.New("user not found")

	ErrIssueToken = errors.New("unable to issue token")
)
