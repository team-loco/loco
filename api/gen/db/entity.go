package db

import (
	"time"

	"github.com/google/uuid"
)

type Entity struct {
	Type EntityType // e.g. "user", "resource", "workspace", "organization"
	ID   uuid.UUID  // unique identifier for the entity
}

type EntityScope struct {
	EntityType EntityType `json:"entity_type"`
	EntityID   uuid.UUID  `json:"entity_id"`
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
	EntityID   uuid.UUID     `json:"entity_id"`
	Scopes     []EntityScope `json:"scopes"`
	ExpiresAt  time.Time     `json:"expires_at"`
}
