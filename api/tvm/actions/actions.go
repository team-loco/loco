package actions

import "github.com/team-loco/loco/api/gen/db"

// Action represents a permission to perform an operation on an entity of a given type.
type Action struct {
	entityType db.EntityType
	scope      db.Scope
	entityID   int64
}

// The following actions do not have an Action listed below because they are publicly accessible:
// - CheckSubdomainAvailability
// - CreateUser
// - Logout
var (
	// apps

	// ListApps requires workspace:read
	ListApps = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeRead,
	}
	// CreateApp requires workspace:write.
	CreateApp = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeWrite,
	}
	// GetApp requires app:read.
	GetApp = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeRead,
	}
	// GetAppStatus requires app:read.
	GetAppStatus = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeRead,
	}
	// StreamLogs requires app:read.
	StreamLogs = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeRead,
	}
	// GetEvents requires app:read.
	GetEvents = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeRead,
	}
	// UpdateApp requires app:write.
	UpdateApp = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeWrite,
	}
	// UpdateAppEnv requires app:write.
	UpdateAppEnv = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeWrite,
	}
	// DeployApp requires app:write.
	DeployApp = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeWrite,
	}
	// ScaleApp requires app:write.
	ScaleApp = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeWrite,
	}
	// UpdateDeploymentStatus requires app:write.
	UpdateDeploymentStatus = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeWrite,
	}
	// RestartApp requires app:write.
	RestartApp = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeWrite,
	}
	// StopApp requires app:write.
	StopApp = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeWrite,
	}
	// DeleteApp requires app:admin.
	DeleteApp = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeAdmin,
	}

	// deployments

	// ListDeployments requires app:read.
	ListDeployments = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeRead,
	}
	// GetDeployment requires app:read.
	GetDeployment = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeRead,
	}
	// StreamDeployment requires app:read.
	StreamDeployment = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeRead,
	}
	// CreateDeployment requires app:write.
	CreateDeployment = Action{
		entityType: db.EntityTypeApp,
		scope:      db.ScopeWrite,
	}

	// orgs
	// ListOrgs requires org:read.
	ListOrgs = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeRead,
	}
	// CreateOrg requires user:write. (user scopes are only granted when the user is onboarded, so this kinda exists as a check to ensure the user is onboarded)
	CreateOrg = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeWrite,
	}
	// GetOrg requires organization:read.
	GetOrg = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeRead,
	}
	// UpdateOrg requires organization:write.
	UpdateOrg = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeWrite,
	}
	// DeleteOrg requires organization:admin.
	DeleteOrg = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeAdmin,
	}
	// AddOrgMember requires organization:write.
	AddOrgMember = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeWrite,
	}
	// RemoveOrgMember requires organization:admin.
	RemoveOrgMember = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeAdmin,
	}
	// ListOrgMembers requires organization:read.
	ListOrgMembers = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeRead,
	}
	// // UpdateOrgMemberRole requires organization:admin.
	// tvm will verify role updates instead of the api.
	// UpdateOrgMemberRole = Action{
	// 	entityType: db.EntityTypeOrganization,
	// 	scope:      db.ScopeAdmin,
	// }

	// users

	// GetUser requires user:read.
	GetUser = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeRead,
	}
	// GetCurrentUser requires user:read.
	GetCurrentUser = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeRead,
	}
	// GetCurrentUserOrgs requires user:read.
	GetCurrentUserOrgs = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeRead,
	}
	// GetCurrentUserWorkspaces requires user:read.
	GetCurrentUserWorkspaces = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeRead,
	}
	// UpdateUser requires user:write.
	UpdateUser = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeWrite,
	}
	// ListUsers requires system:read
	ListUsers = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeRead,
	}
	// DeleteUser requires user:admin.
	DeleteUser = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeAdmin,
	}

	// workspace
	// ListWorkspaces requires organization:read (assuming workspaces are scoped to orgs).
	ListWorkspaces = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeRead,
	}
	// CreateWorkspace requires organization:write.
	CreateWorkspace = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeWrite,
	}
	// GetWorkspace requires workspace:read.
	GetWorkspace = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeRead,
	}
	// UpdateWorkspace requires workspace:write.
	UpdateWorkspace = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeWrite,
	}
	// DeleteWorkspace requires workspace:admin.
	DeleteWorkspace = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeAdmin,
	}
	// AddWorkspaceMember requires workspace:write.
	AddWorkspaceMember = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeWrite,
	}
	// RemoveWorkspaceMember requires workspace:admin.
	RemoveWorkspaceMember = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeAdmin,
	}
	// ListWorkspaceMembers requires workspace:read.
	ListWorkspaceMembers = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeRead,
	}
	// // UpdateWorkspaceMemberRole requires workspace:admin.
	// tvm will verify role updates instead of the api.
	// UpdateWorkspaceMemberRole = Action{
	// 	entityType: db.EntityTypeWorkspace,
	// 	scope:      db.ScopeAdmin,
	// }
)

func New(a Action, entityID int64) db.EntityScope {
	return db.EntityScope{
		Entity: db.Entity{
			Type: a.entityType,
			ID:   entityID,
		},
		Scope: a.scope,
	}
}
