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

type Scope string

const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
	ScopeAdmin Scope = "admin"
)
