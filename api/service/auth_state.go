package service

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
)

// Callers start sign-ins before they sign in. maxAuthStates bounds the sign-ins in progress and
// maxAuthStartsPerClient those one client address has started and not finished. Concurrent starts
// can each pass the check, so both bounds are approximate.
const (
	maxAuthStates          = 100_000
	maxAuthStartsPerClient = 30
)

var (
	errAuthStateNotFound = errors.New("auth state not found")
	errAuthStateFull     = errors.New("auth state store is full")
	errAuthStateExists   = errors.New("auth state already exists")
)

// authStore holds short-lived sign-in state that every API replica must see.
type authStore interface {
	// Get returns errAuthStateNotFound when the key is absent or expired.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores an entry that belongs to a sign-in already started or to a signed-in caller.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Start stores the value for a new sign-in by client. It returns errAuthStateExists when the key is
	// live, and errAuthStateFull when the store or client holds as many live sign-ins as allowed.
	Start(ctx context.Context, key string, value []byte, ttl time.Duration, client string) error
	// Restore puts back a sign-in that Take removed, counted against client again; the bounds do not apply.
	Restore(ctx context.Context, key string, value []byte, ttl time.Duration, client string) error
	// Take removes a live entry and returns its value, or errAuthStateNotFound when another call took it first.
	Take(ctx context.Context, key string) ([]byte, error)
}

// pgAuthStore keeps sign-in state in the auth_states table.
type pgAuthStore struct {
	queries      genDb.Querier
	maxEntries   int32
	maxPerClient int32
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
	return s.Restore(ctx, key, value, ttl, "")
}

func (s pgAuthStore) Restore(ctx context.Context, key string, value []byte, ttl time.Duration, client string) error {
	err := s.queries.SetAuthState(ctx, genDb.SetAuthStateParams{
		Key: key, Value: value, Client: client, TtlSeconds: ttl.Seconds(),
	})
	if err != nil {
		return fmt.Errorf("set auth state: %w", err)
	}
	return nil
}

func (s pgAuthStore) Start(ctx context.Context, key string, value []byte, ttl time.Duration, client string) error {
	stored, err := s.queries.StartAuthState(ctx, genDb.StartAuthStateParams{
		Key: key, Value: value, Client: client, TtlSeconds: ttl.Seconds(),
		MaxEntries: s.maxEntries, MaxPerClient: s.maxPerClient,
	})
	if err != nil {
		return fmt.Errorf("start auth state: %w", err)
	}
	if stored == 1 {
		return nil
	}
	_, err = s.Get(ctx, key)
	if errors.Is(err, errAuthStateNotFound) {
		return errAuthStateFull
	}
	if err != nil {
		return err
	}
	return errAuthStateExists
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

// authClient names the client that starts a sign-in. An IPv6 client is its /64, the block one
// subscriber usually holds.
func authClient(addr netip.Addr) string {
	addr = addr.Unmap()
	if !addr.Is6() {
		return addr.String()
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return prefix.String()
}
