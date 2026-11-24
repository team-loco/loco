package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
)

var errAuthStateNotFound = errors.New("auth state not found")

// authStore holds short-lived sign-in state that every API replica must see.
type authStore interface {
	// Get returns errAuthStateNotFound when the key is absent or expired.
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// SetIfAbsent stores the value only when the key is absent or expired, reporting whether it did.
	SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	// Take removes the key, reporting whether this call removed a live entry.
	Take(ctx context.Context, key string) (bool, error)
}

// pgAuthStore keeps sign-in state in the auth_states table.
type pgAuthStore struct {
	queries genDb.Querier
}

func (s pgAuthStore) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := s.queries.GetAuthState(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errAuthStateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get auth state: %w", err)
	}
	return value, nil
}

func (s pgAuthStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	err := s.queries.SetAuthState(ctx, genDb.SetAuthStateParams{Key: key, Value: value, TtlSeconds: ttl.Seconds()})
	if err != nil {
		return fmt.Errorf("set auth state: %w", err)
	}
	return nil
}

func (s pgAuthStore) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	stored, err := s.queries.SetAuthStateIfAbsent(ctx, genDb.SetAuthStateIfAbsentParams{
		Key: key, Value: value, TtlSeconds: ttl.Seconds(),
	})
	if err != nil {
		return false, fmt.Errorf("set auth state if absent: %w", err)
	}
	return stored == 1, nil
}

func (s pgAuthStore) Take(ctx context.Context, key string) (bool, error) {
	taken, err := s.queries.TakeAuthState(ctx, key)
	if err != nil {
		return false, fmt.Errorf("take auth state: %w", err)
	}
	return taken == 1, nil
}
