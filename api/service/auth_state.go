package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
)

// maxAuthStates is the most started sign-ins the store keeps; a new start deletes the oldest beyond it.
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
	// Restore puts back a started sign-in that Take removed.
	Restore(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Take removes a live entry and returns its value, or errAuthStateNotFound when another call took it first.
	Take(ctx context.Context, key string) ([]byte, error)
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
	return s.set(ctx, key, value, ttl, false)
}

func (s pgAuthStore) Restore(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.set(ctx, key, value, ttl, true)
}

func (s pgAuthStore) set(ctx context.Context, key string, value []byte, ttl time.Duration, started bool) error {
	err := s.queries.SetAuthState(ctx, genDb.SetAuthStateParams{
		Key: key, Value: value, Started: started, TtlSeconds: ttl.Seconds(),
	})
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
