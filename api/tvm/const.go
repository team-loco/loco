package tvm

import (
	"errors"

	queries "github.com/team-loco/loco/api/gen/db"
)

const issuer = "loco-api"

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
	ErrImproperUsage             = errors.New("improper usage of token vending machine")

	ErrTokenExpired  = errors.New("token has expired")
	ErrTokenNotFound = errors.New("token not found")
	ErrExchange      = errors.New("exchange with external provider failed")

	ErrUserNotFound   = errors.New("user not found")
	ErrEntityNotFound = errors.New("entity not found or invalid entity")

	ErrIssueToken = errors.New("unable to issue token")
)
