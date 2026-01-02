package db

import "time"

type Entity struct {
	Type EntityType // e.g. "user", "app", "workspace", "organization"
	ID   int64      // unique identifier for the entity
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

// TokenHead represents a token without the actual token string.
type TokenHead struct {
	Name       string        `json:"name"`
	EntityType EntityType    `json:"entity_type"`
	EntityID   int64         `json:"entity_id"`
	Scopes     []EntityScope `json:"scopes"`
	ExpiresAt  time.Time     `json:"expires_at"`
}
