package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
)

// maxAuthStates bounds the live sign-in entries, which callers create before they sign in.
const maxAuthStates = 100_000

var (
	errAuthStateNotFound = errors.New("auth state not found")
	errAuthStateFull     = errors.New("auth state store is full")
)

// authStore holds short-lived sign-in state that every API replica must see.
type authStore interface {
	// Get returns errAuthStateNotFound when the key is absent or expired.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set returns errAuthStateFull for a new key while maxEntries live entries exist.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// SetIfAbsent stores the value only when the key is absent or expired and the store has room,
	// reporting whether it did.
	SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	// Take removes a live entry and returns its value, or errAuthStateNotFound when another call took it first.
	Take(ctx context.Context, key string) ([]byte, error)
}

// pgAuthStore keeps sign-in state in the auth_states table, holding at most maxEntries live rows.
type pgAuthStore struct {
	queries    genDb.Querier
	maxEntries int32
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
	stored, err := s.queries.SetAuthState(ctx, genDb.SetAuthStateParams{
		Key: key, Value: value, TtlSeconds: ttl.Seconds(), MaxEntries: s.maxEntries,
	})
	if err != nil {
		return fmt.Errorf("set auth state: %w", err)
	}
	if stored == 0 {
		return errAuthStateFull
	}
	return nil
}

func (s pgAuthStore) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	stored, err := s.queries.SetAuthStateIfAbsent(ctx, genDb.SetAuthStateIfAbsentParams{
		Key: key, Value: value, TtlSeconds: ttl.Seconds(), MaxEntries: s.maxEntries,
	})
	if err != nil {
		return false, fmt.Errorf("set auth state if absent: %w", err)
	}
	return stored == 1, nil
}

func (s pgAuthStore) Take(ctx context.Context, key string) ([]byte, error) {
	value, err := s.queries.TakeAuthState(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errAuthStateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("take auth state: %w", err)
	}
	return value, nil
}
