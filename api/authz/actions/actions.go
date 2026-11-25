package actions

import (
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/gen/db"
)

// Action represents a permission to perform an operation on an entity of a given type.
type Action struct {
	entityType db.EntityType
	scope      db.Scope
}

// Logout is public and has no Action.
var (
	// resources

	// ListResources requires workspace:read
	ListResources = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeRead,
	}
	// ListRegions requires workspace:read.
	ListRegions = Action{
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
	// GetResourceEnv requires resource:read.
	GetResourceEnv = Action{
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
	// ScaleResource requires resource:write.
	ScaleResource = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	// RestartResource requires resource:write.
	RestartResource = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	// DeleteResource requires resource:admin.
	DeleteResource = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeAdmin,
	}
	// TransferPartial requires resource:admin.
	TransferPartial = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeAdmin,
	}

	// observability

	// ReadWorkspaceTelemetry requires workspace:read.
	ReadWorkspaceTelemetry = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeRead,
	}
	// ReadSystemTelemetry requires system:read.
	ReadSystemTelemetry = Action{entityType: db.EntityTypeSystem, scope: db.ScopeRead}
	// ReadResourceTelemetry requires resource:read.
	ReadResourceTelemetry = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeRead,
	}

	// deployments

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
	// CancelDeployment requires resource:write.
	CancelDeployment = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}

	CreateBuild = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}
	GetBuild = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeRead,
	}
	ListBuilds = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeRead,
	}
	CancelBuild = Action{
		entityType: db.EntityTypeResource,
		scope:      db.ScopeWrite,
	}

	// orgs
	// CreateOrg requires user:write, which only onboarded users hold.
	CreateOrg = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeWrite,
	}
	// GetOrg requires organization:member.
	GetOrg = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeMember,
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
	// SetOrgMemberRole requires organization:admin.
	SetOrgMemberRole = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeAdmin,
	}
	// RemoveOrgMember requires organization:admin.
	RemoveOrgMember = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeAdmin,
	}
	// InviteOrgMember requires organization:admin.
	InviteOrgMember = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeAdmin,
	}
	// RevokeInvitation requires organization:admin.
	RevokeInvitation = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeAdmin,
	}
	// ResendInvitation requires organization:admin.
	ResendInvitation = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeAdmin,
	}
	// ListInvitations requires organization:member.
	ListInvitations = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeMember,
	}
	// ListOrgMembers requires organization:member.
	ListOrgMembers = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeMember,
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
	// SetUserStatus requires system:admin.
	SetUserStatus = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeAdmin,
	}
	// ListUserSessions requires user:read.
	ListUserSessions = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeRead,
	}
	// RevokeUserSession requires user:admin.
	RevokeUserSession = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeAdmin,
	}
	// RevokeUserSessions requires system:admin.
	RevokeUserSessions = Action{entityType: db.EntityTypeSystem, scope: db.ScopeAdmin}
	// DeleteUser requires user:admin.
	DeleteUser = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeAdmin,
	}

	// workspace
	// ListWorkspaces requires organization:member.
	ListWorkspaces = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeMember,
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
	ManageWorkspaceWebhooks = Action{
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

	// ListClusters requires system:read.
	ListClusters = Action{entityType: db.EntityTypeSystem, scope: db.ScopeRead}
	// ManageClusters requires system:admin.
	ManageClusters = Action{entityType: db.EntityTypeSystem, scope: db.ScopeAdmin}

	// ListSystemRoles requires system:read.
	ListSystemRoles = Action{entityType: db.EntityTypeSystem, scope: db.ScopeRead}
	// ManageSystemRoles requires system:admin.
	ManageSystemRoles = Action{entityType: db.EntityTypeSystem, scope: db.ScopeAdmin}

	// domains

	ListAppDomains = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeRead,
	}

	// environments

	// ListEnvironments requires workspace:read.
	ListEnvironments = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeRead,
	}
	// CreateEnvironment requires workspace:write.
	CreateEnvironment = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeWrite,
	}
	// GetEnvironment requires workspace:read (looked up via env's workspace_id).
	GetEnvironment = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeRead,
	}
	// Apply requires workspace:write; a delete in the plan also needs DeleteResource.
	Apply = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeWrite,
	}
	// Plan requires workspace:read; it writes nothing.
	Plan = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeRead,
	}
	// UpdateEnvironment requires workspace:write.
	UpdateEnvironment = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeWrite,
	}
	// DeleteEnvironment requires workspace:admin.
	DeleteEnvironment = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeAdmin,
	}

	// secrets

	// SetSecrets requires workspace:write on the environment's workspace.
	SetSecrets = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeWrite,
	}
	// DeleteSecrets requires workspace:write.
	DeleteSecrets = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeWrite,
	}
	// ListSecrets requires workspace:read.
	ListSecrets = Action{
		entityType: db.EntityTypeWorkspace,
		scope:      db.ScopeRead,
	}
	// RewrapEnvironmentKeys requires system:admin.
	RewrapEnvironmentKeys = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeAdmin,
	}

	// orgs (additional)

	// SuspendOrg requires system:admin.
	SuspendOrg = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeAdmin,
	}
	// ResumeOrg requires system:admin.
	ResumeOrg = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeAdmin,
	}
	// GetOrgLimits requires organization:member.
	GetOrgLimits = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeMember,
	}
	// SetOrgLimits requires system:admin.
	SetOrgLimits = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeAdmin,
	}

	// ListOrgs requires system:read.
	ListOrgs = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeRead,
	}
	// ListUserOrgs requires user:read (to list orgs for a specific user).
	ListUserOrgs = Action{
		entityType: db.EntityTypeUser,
		scope:      db.ScopeRead,
	}

	// Token management actions are dynamically defined.

	ListOrgAuditEvents = Action{
		entityType: db.EntityTypeOrganization,
		scope:      db.ScopeAdmin,
	}
	StreamWorkspaceEvents        = Action{entityType: db.EntityTypeWorkspace, scope: db.ScopeRead}
	ListWorkspaceAuditEvents     = Action{entityType: db.EntityTypeWorkspace, scope: db.ScopeAdmin}
	ListResourceAuditEvents      = Action{entityType: db.EntityTypeResource, scope: db.ScopeRead}
	ListResourceAuditAdminEvents = Action{entityType: db.EntityTypeResource, scope: db.ScopeAdmin}
	// ListInstallationEvents requires system:read.
	ListInstallationEvents = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeRead,
	}
	// StreamEvents requires system:read.
	StreamEvents = Action{
		entityType: db.EntityTypeSystem,
		scope:      db.ScopeRead,
	}
)

func New(a Action, entityID uuid.UUID) db.EntityScope {
	return db.EntityScope{
		EntityType: a.entityType,
		EntityID:   entityID,
		Scope:      a.scope,
	}
}

// NewSystem builds an EntityScope for system-level actions where entity ID is not meaningful.
func NewSystem(a Action) db.EntityScope {
	return db.EntityScope{
		EntityType: a.entityType,
		EntityID:   uuid.Nil,
		Scope:      a.scope,
	}
}
