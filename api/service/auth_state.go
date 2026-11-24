package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
)

// maxAuthStates is the most started entries the store keeps; a new start deletes the oldest beyond it.
// A device login holds two started entries, its user code and its device row, and eviction can delete
// one without the other. Without its user code the login can no longer be approved and polls as pending
// until it expires; without its device row it polls as expired.
const maxAuthStates = 100_000

var (
	errAuthStateNotFound = errors.New("auth state not found")
	errAuthStateExists   = errors.New("auth state already exists")
)

// authStore holds short-lived sign-in state that every API replica must see.
type authStore interface {
	// Get returns errAuthStateNotFound when the key is absent or expired.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores an entry that belongs to a sign-in already started or to a signed-in caller.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Start stores the value for a new sign-in, deleting the oldest started sign-ins beyond maxEntries.
	// It returns errAuthStateExists when the key is live.
	Start(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Take removes a live entry and returns it, or errAuthStateNotFound when another call took it first.
	Take(ctx context.Context, key string) (authEntry, error)
}

// authEntry is a taken entry: its value and how long it had left to live.
type authEntry struct {
	value []byte
	ttl   time.Duration
}

// pgAuthStore keeps sign-in state in the auth_states table.
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
	err := s.queries.SetAuthState(ctx, genDb.SetAuthStateParams{Key: key, Value: value, TtlSeconds: ttl.Seconds()})
	if err != nil {
		return fmt.Errorf("set auth state: %w", err)
	}
	return nil
}

func (s pgAuthStore) Start(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	stored, err := s.queries.StartAuthState(ctx, genDb.StartAuthStateParams{
		Key: key, Value: value, TtlSeconds: ttl.Seconds(), Keep: s.maxEntries - 1,
	})
	if err != nil {
		return fmt.Errorf("start auth state: %w", err)
	}
	if stored == 0 {
		return errAuthStateExists
	}
	return nil
}

func (s pgAuthStore) Take(ctx context.Context, key string) (authEntry, error) {
	row, err := s.queries.TakeAuthState(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return authEntry{}, errAuthStateNotFound
	}
	if err != nil {
		return authEntry{}, fmt.Errorf("take auth state: %w", err)
	}
	return authEntry{value: row.Value, ttl: time.Duration(row.TtlSeconds * float64(time.Second))}, nil
}
