package tvm_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz/authztest"
	"github.com/team-loco/loco/api/db/dbtest"
	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
)

// sessionEntry is the in-memory representation of a session_token row.
type sessionEntry struct {
	id          uuid.UUID
	userID      uuid.UUID
	accessHash  string
	refreshHash string
	lastUsedAt  time.Time
	kind        queries.SessionKind
}

// TestingQueries is an in-memory implementation of queries.Querier for unit tests.
// It supports the session and user-scope operations these tests exercise.
type TestingQueries struct {
	queries.Querier
	sessions  map[uuid.UUID]*sessionEntry
	byAccess  map[string]uuid.UUID
	byRefresh map[string]uuid.UUID
	used      map[string]uuid.UUID
	usedAt    map[string]time.Time
}

func newTestingQueries() *TestingQueries {
	return &TestingQueries{
		sessions:  make(map[uuid.UUID]*sessionEntry),
		byAccess:  make(map[string]uuid.UUID),
		byRefresh: make(map[string]uuid.UUID),
		used:      make(map[string]uuid.UUID),
		usedAt:    make(map[string]time.Time),
	}
}

func (*TestingQueries) GetUserScopes(_ context.Context, userID uuid.UUID) ([]queries.GetUserScopesRow, error) {
	return authztest.UserScopes(userID)
}

// --- Session token mock implementations ---

func (tq *TestingQueries) CreateSessionToken(_ context.Context, params queries.CreateSessionTokenParams) error {
	entry := &sessionEntry{
		id:          params.ID,
		userID:      params.UserID,
		accessHash:  params.AccessTokenHash,
		refreshHash: params.RefreshTokenHash,
		lastUsedAt:  time.Now(),
		kind:        params.Kind,
	}
	tq.sessions[params.ID] = entry
	tq.byAccess[params.AccessTokenHash] = params.ID
	tq.byRefresh[params.RefreshTokenHash] = params.ID
	return nil
}

func (tq *TestingQueries) GetSessionWithScopesByAccessToken(
	ctx context.Context,
	accessTokenHash string,
) (queries.GetSessionWithScopesByAccessTokenRow, error) {
	id, ok := tq.byAccess[accessTokenHash]
	if !ok {
		return queries.GetSessionWithScopesByAccessTokenRow{}, pgx.ErrNoRows
	}
	e := tq.sessions[id]
	rows, err := tq.GetUserScopes(ctx, e.userID)
	if err != nil {
		return queries.GetSessionWithScopesByAccessTokenRow{}, err
	}
	scopes := make([]queries.EntityScope, len(rows))
	for i, row := range rows {
		scopes[i] = queries.EntityScope(row)
	}
	encoded, err := json.Marshal(scopes)
	if err != nil {
		return queries.GetSessionWithScopesByAccessTokenRow{}, err
	}
	return queries.GetSessionWithScopesByAccessTokenRow{
		ID:         e.id,
		UserID:     e.userID,
		LastUsedAt: e.lastUsedAt,
		Scopes:     encoded,
	}, nil
}

func (tq *TestingQueries) GetSessionByRefreshToken(
	_ context.Context,
	refreshTokenHash string,
) (queries.GetSessionByRefreshTokenRow, error) {
	id, current := tq.byRefresh[refreshTokenHash]
	if !current {
		id = tq.used[refreshTokenHash]
	}
	e, ok := tq.sessions[id]
	if !ok {
		return queries.GetSessionByRefreshTokenRow{}, pgx.ErrNoRows
	}
	row := queries.GetSessionByRefreshTokenRow{ID: e.id, UserID: e.userID, Kind: e.kind, Used: !current}
	if !current {
		usedAt := tq.usedAt[refreshTokenHash]
		row.UsedAt = &usedAt
	}
	return row, nil
}

func (tq *TestingQueries) RotateSessionToken(
	_ context.Context,
	params queries.RotateSessionTokenParams,
) (int64, error) {
	e, ok := tq.sessions[params.ID]
	if !ok || e.refreshHash != params.OldRefreshTokenHash {
		return 0, nil
	}
	delete(tq.byAccess, e.accessHash)
	delete(tq.byRefresh, e.refreshHash)
	tq.used[e.refreshHash] = e.id
	tq.usedAt[e.refreshHash] = time.Now()
	e.accessHash = params.AccessTokenHash
	e.refreshHash = params.RefreshTokenHash
	tq.byAccess[e.accessHash] = e.id
	tq.byRefresh[e.refreshHash] = e.id
	return 1, nil
}

func (*TestingQueries) TouchSessionLastUsed(_ context.Context, _ uuid.UUID) error { return nil }

func (tq *TestingQueries) DeleteSessionToken(_ context.Context, id uuid.UUID) error {
	e, ok := tq.sessions[id]
	if !ok {
		return nil
	}
	delete(tq.byAccess, e.accessHash)
	delete(tq.byRefresh, e.refreshHash)
	delete(tq.sessions, id)
	return nil
}

func (tq *TestingQueries) DeleteSessionTokenByAccessHash(ctx context.Context, accessTokenHash string) error {
	id, ok := tq.byAccess[accessTokenHash]
	if !ok {
		return nil
	}
	return tq.DeleteSessionToken(ctx, id)
}

func (*TestingQueries) DeleteExpiredSessionTokens(_ context.Context) error { return nil }

func (*TestingQueries) DeleteExpiredUsedRefreshTokens(_ context.Context) error { return nil }

func (*TestingQueries) DeleteExpiredAPITokens(_ context.Context) error { return nil }

// --- Test helpers ---

func session(t *testing.T, machine *tvm.VendingMachine, userID uuid.UUID) (string, string) {
	t.Helper()
	access, refresh, err := machine.IssueSession(t.Context(), userID, netip.Addr{}, "")
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	return access, refresh
}

func testConfig() tvm.Config {
	return tvm.Config{
		MaxAPITokenDuration:         24 * time.Hour,
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: 24 * time.Hour,
		LastUsedUpdateInterval:      5 * time.Minute,
	}
}

func TestSessionCarriesLiveUserScopes(t *testing.T) {
	t.Parallel()
	machine := tvm.NewVendingMachine(newTestingQueries(), testConfig())
	access, _ := session(t, machine, authztest.User3)
	principal, err := machine.GetToken(t.Context(), access)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if principal.Entity.Type != queries.EntityTypeUser || principal.Entity.ID != authztest.User3 {
		t.Fatalf("entity = %+v", principal.Entity)
	}
	want := queries.EntityScope{
		EntityType: queries.EntityTypeOrganization,
		EntityID:   authztest.Org1,
		Scope:      queries.ScopeWrite,
	}
	found := false
	for _, s := range principal.Scopes {
		if s == want {
			found = true
		}
	}
	if !found || len(principal.Scopes) != 5 {
		t.Fatalf("scopes = %+v", principal.Scopes)
	}
}

type racingRefreshQueries struct {
	*TestingQueries
}

func (rq racingRefreshQueries) RotateSessionToken(
	ctx context.Context,
	params queries.RotateSessionTokenParams,
) (int64, error) {
	competing := params
	competing.AccessTokenHash = "competing-access"
	competing.RefreshTokenHash = "competing-refresh"
	if _, err := rq.TestingQueries.RotateSessionToken(ctx, competing); err != nil {
		return 0, err
	}
	return rq.TestingQueries.RotateSessionToken(ctx, params)
}

func TestRefreshRotatesTokens(t *testing.T) {
	t.Parallel()
	machine := tvm.NewVendingMachine(newTestingQueries(), testConfig())
	_, refreshToken := session(t, machine, authztest.User1)

	access, newRefresh, err := machine.Refresh(t.Context(), refreshToken, queries.SessionKindCli)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if access == "" || newRefresh == "" || newRefresh == refreshToken {
		t.Fatalf("refresh did not issue a new token pair")
	}

	if _, _, err := machine.Refresh(
		t.Context(), refreshToken, queries.SessionKindCli,
	); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("reusing a rotated refresh token: got %v, want ErrInvalidExpiredToken", err)
	}
}

func TestRefreshWithinTheReuseGraceKeepsTheSession(t *testing.T) {
	t.Parallel()
	tq := newTestingQueries()
	cfg := testConfig()
	cfg.RefreshReuseGrace = time.Minute
	machine := tvm.NewVendingMachine(tq, cfg)
	_, refreshToken := session(t, machine, authztest.User1)
	if _, _, err := machine.Refresh(t.Context(), refreshToken, queries.SessionKindCli); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, _, err := machine.Refresh(
		t.Context(), refreshToken, queries.SessionKindCli,
	); !errors.Is(err, tvm.ErrRefreshRotated) {
		t.Fatalf("refresh with a just-rotated token: got %v, want ErrRefreshRotated", err)
	}
	if len(tq.sessions) != 1 {
		t.Fatalf("sessions = %d, want the session kept", len(tq.sessions))
	}
}

func TestRefreshLosingRaceRevokesSession(t *testing.T) {
	t.Parallel()
	tq := newTestingQueries()
	machine := tvm.NewVendingMachine(racingRefreshQueries{tq}, testConfig())
	_, refreshToken := session(t, machine, authztest.User1)

	if _, _, err := machine.Refresh(
		t.Context(), refreshToken, queries.SessionKindCli,
	); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("losing refresh: got %v, want ErrInvalidExpiredToken", err)
	}
	if len(tq.sessions) != 0 {
		t.Fatalf("sessions = %d, want the session revoked", len(tq.sessions))
	}
}

func TestDatabaseFailureIsNotAnInvalidToken(t *testing.T) {
	t.Parallel()
	pool := dbtest.ClosedPool(t)
	machine := tvm.NewVendingMachine(queries.New(pool), tvm.Config{})
	for _, token := range []string{"loco_s_unknown", "loco_k_unknown"} {
		if _, err := machine.GetToken(t.Context(), token); err == nil || errors.Is(err, tvm.ErrInvalidExpiredToken) {
			t.Errorf("GetToken(%s) on a closed pool: got %v, want the database error", token, err)
		}
	}
	_, _, err := machine.Refresh(t.Context(), "loco_r_unknown", queries.SessionKindCli)
	if err == nil || errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Errorf("Refresh on a closed pool: got %v, want the database error", err)
	}
}

type cleanupQueries struct {
	*TestingQueries
	sweeps chan struct{}
}

func (c cleanupQueries) DeleteExpiredSessionTokens(context.Context) error {
	select {
	case c.sweeps <- struct{}{}:
	default:
	}
	return nil
}

func TestRunCleanupSweepsUntilContextIsCanceled(t *testing.T) {
	t.Parallel()
	q := cleanupQueries{newTestingQueries(), make(chan struct{}, 1)}
	machine := tvm.NewVendingMachine(q, testConfig())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		machine.RunCleanup(ctx, time.Millisecond)
		close(done)
	}()

	select {
	case <-q.sweeps:
	case <-time.After(5 * time.Second):
		t.Fatal("no sweep ran")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunCleanup did not return after its context was canceled")
	}
}
