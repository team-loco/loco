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
	// resources

	// ListResources requires workspace:read
	ListResources = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeRead,
	}
	// CreateResource requires workspace:write.
	CreateResource = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeWrite,
	}
	// GetResource requires resource:read.
	GetResource = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeRead,
	}
	// GetResourceStatus requires resource:read.
	GetResourceStatus = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeRead,
	}
	// StreamResourceLogs requires resource:read.
	StreamResourceLogs = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeRead,
	}
	// GetResourceEvents requires resource:read.
	GetResourceEvents = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeRead,
	}
	AddDomain = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	UpdateDomain = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	RemoveDomain = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	SetPrimaryDomain = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	// UpdateResource requires resource:write.
	UpdateResource = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	// UpdateResourceEnv requires resource:write.
	UpdateResourceEnv = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	// DeployResource requires resource:write.
	DeployResource = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	// ScaleResource requires resource:write.
	ScaleResource = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	// UpdateDeploymentStatus requires resource:write.
	UpdateDeploymentStatus = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	// RestartResource requires resource:write.
	RestartResource = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	// StopResource requires resource:write.
	StopResource = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	// DeleteResource requires resource:admin.
	DeleteResource = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeAdmin,
	}

	// deployments

	// ListDeployments requires resource:read.
	ListDeployments = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeRead,
	}
	// GetDeployment requires resource:read.
	GetDeployment = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeRead,
	}
	// StreamDeployment requires resource:read.
	StreamDeployment = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeRead,
	}
	// CreateDeployment requires resource:write.
	CreateDeployment = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	// DeleteDeployment requires resource:write
	DeleteDeployment = Action{
		entityType: db.EntityTypeResource,
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

	// domains

	// CreatePlatformDomain requires system:admin.
	CreatePlatformDomain = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeAdmin,
	}
	// UpdatePlatformDomain requires system:admin.
	UpdatePlatformDomain = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeAdmin,
	}
	// DeletePlatformDomain requires system:admin.
	DeletePlatformDomain = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeAdmin,
	}
	// ListLocoOwnedDomains requires system:admin.
	ListLocoOwnedDomains = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeAdmin,
	}

	// orgs (additional)

	// ListUserOrgs requires user:read (to list orgs for a specific user).
	ListUserOrgs = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeRead,
	}

	// registry

	// GetGitlabToken requires user:read (to access registry).
	GetGitlabToken = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeRead,
	}

	// Token management actions are dynamically defined.
)

func New(a Action, entityID int64) db.EntityScope {
	return db.EntityScope{
		EntityType: a.entityType,
		EntityID:   entityID,
		Scope:      a.scope,
	}
}
