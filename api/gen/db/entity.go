package db

type Entity struct {
	Type EntityType // e.g. "user", "app", "workspace", "organization"
	ID   int64      // unique identifier for the entity
}

func (e Entity) WithScope(scope Scope) EntityScope {
	return EntityScope{
		Entity: e,
		Scope:  scope,
	}
}

type EntityScope struct {
	Entity Entity // associated entity
	Scope  Scope  // e.g. "read", "write", "admin"
}

// EntityScopeFromUserScope returns a UserScope (or a scope that a user has on an entity)
// as an entity scope. It discards the user ID information, since an EntityScope only contains
// the entity and the scope and not the user that has that entity and scope.
func EntityScopeFromUserScope(us UserScope) EntityScope {
	return EntityScope{
		Entity: Entity{
			Type: us.EntityType,
			ID:   us.EntityID,
		},
		Scope: Scope(us.Scope),
	}
}

// EntityScopesFromUserScopes converts a slice of UserScope to a slice of EntityScope. See
// EntityScopeFromUserScope for more details abt what this actually means.
func EntityScopesFromUserScopes(uss []UserScope) []EntityScope {
	ess := make([]EntityScope, len(uss))
	for i, us := range uss {
		ess[i] = EntityScopeFromUserScope(us)
	}
	return ess
}

type Scope string

const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
	ScopeAdmin Scope = "admin"
)
