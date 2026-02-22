package db

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type Entity struct {
	Type EntityType  // e.g. "user", "resource", "workspace", "organization"
	ID   pgtype.UUID // unique identifier for the entity
}

type EntityScope struct {
	EntityType EntityType  `json:"entity_type"`
	EntityID   pgtype.UUID `json:"entity_id"`
	Scope      Scope       `json:"scope"`
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
	EntityID   pgtype.UUID   `json:"entity_id"`
	Scopes     []EntityScope `json:"scopes"`
	ExpiresAt  time.Time     `json:"expires_at"`
}
