package db

import (
	"fmt"
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

// ScanNull implements pgtype.CompositeIndexScanner.
func (es *EntityScope) ScanNull() error {
	return fmt.Errorf("cannot scan NULL into EntityScope")
}

// ScanIndex implements pgtype.CompositeIndexScanner.
// Field order matches the entity_scope composite type definition: (scope TEXT, entity_type entity_type, entity_id UUID)
func (es *EntityScope) ScanIndex(i int) any {
	switch i {
	case 0:
		return &es.Scope
	case 1:
		return &es.EntityType
	case 2:
		return &es.EntityID
	}
	return nil
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
