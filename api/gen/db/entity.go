package db

type Entity struct {
	Type EntityType // e.g. "user", "app", "workspace", "organization"
	ID   int64      // unique identifier for the entity
}

func (e Entity) WithScope(scope Scope) EntityScope {
	return EntityScope{
		EntityType: e.Type,
		EntityID:   e.ID,
		Scope:      scope,
	}
}

type EntityScope struct {
	EntityType EntityType `json:"entity_type"`
	EntityID   int64      `json:"entity_id"`
	Scope      Scope      `json:"scope"`
}

type Scope = string

const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
	ScopeAdmin Scope = "admin"
)
