package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	genDb "github.com/team-loco/loco/api/gen/db"
)

// maxAuthStates is the most started sign-ins the store keeps; a new start deletes the oldest beyond it.
const maxAuthStates = 1_000

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
	// Start stores the entries of one new sign-in, which count and are deleted together, after deleting
	// the oldest started sign-ins beyond maxSignIns. It returns errAuthStateExists when a key is live.
	Start(ctx context.Context, ttl time.Duration, entries ...startEntry) error
	// Take removes a live entry and returns it, or errAuthStateNotFound when another call took it first.
	Take(ctx context.Context, key string) (authEntry, error)
	// Restore puts back an entry Take removed, as part of the sign-in it belonged to.
	Restore(ctx context.Context, key string, entry authEntry) error
}

type startEntry struct {
	key   string
	value []byte
}

// authEntry is a taken entry: its value, the sign-in start it belongs to, and how long it had left.
type authEntry struct {
	value     []byte
	startedAt *time.Time
	ttl       time.Duration
}

// pgAuthStore keeps sign-in state in the auth_states table.
type pgAuthStore struct {
	pool       *pgxpool.Pool
	queries    genDb.Querier
	maxSignIns int32
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
	return s.Restore(ctx, key, authEntry{value: value, ttl: ttl})
}

func (s pgAuthStore) Restore(ctx context.Context, key string, entry authEntry) error {
	err := s.queries.SetAuthState(ctx, genDb.SetAuthStateParams{
		Key: key, Value: entry.value, StartedAt: entry.startedAt, TtlSeconds: entry.ttl.Seconds(),
	})
	if err != nil {
		return fmt.Errorf("set auth state: %w", err)
	}
	return nil
}

func (s pgAuthStore) Start(ctx context.Context, ttl time.Duration, entries ...startEntry) error {
	params := genDb.StartAuthStatesParams{TtlSeconds: ttl.Seconds(), Keep: s.maxSignIns - 1}
	for _, entry := range entries {
		params.Keys = append(params.Keys, entry.key)
		params.Values = append(params.Values, entry.value)
	}
	// A key another start holds uncommitted is skipped once that start commits, with the other
	// entries stored; rolling back leaves none of them.
	return pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		stored, err := genDb.New(tx).StartAuthStates(ctx, params)
		if err != nil {
			return fmt.Errorf("start auth state: %w", err)
		}
		if stored < int64(len(entries)) {
			return errAuthStateExists
		}
		return nil
	})
}

func (s pgAuthStore) Take(ctx context.Context, key string) (authEntry, error) {
	row, err := s.queries.TakeAuthState(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return authEntry{}, errAuthStateNotFound
	}
	if err != nil {
		return authEntry{}, fmt.Errorf("take auth state: %w", err)
	}
	return authEntry{
		value: row.Value, startedAt: row.StartedAt, ttl: time.Duration(row.TtlSeconds * float64(time.Second)),
	}, nil
}
