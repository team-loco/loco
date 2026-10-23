package tvm_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/internal/bearer"
)

func assertBearerFormat(t *testing.T, token, prefix string) {
	t.Helper()
	encoded, ok := strings.CutPrefix(token, prefix)
	if !ok {
		t.Fatalf("token %q lacks prefix %q", token, prefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode %q: %v", token, err)
	}
	if len(raw) != bearer.Bytes {
		t.Fatalf("token %q carries %d random bytes, want %d", token, len(raw), bearer.Bytes)
	}
}

func digestOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func TestIssuedTokensCarryGeneratedBytesAndStoreOnlyHashes(t *testing.T) {
	pool := authtest.NewPool(t)
	q := queries.New(pool)
	machine := tvm.NewVendingMachine(pool, q, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		MaxAPITokenDuration:         time.Hour,
		LastUsedUpdateInterval:      time.Minute,
	})
	t.Cleanup(machine.Close)
	ctx := t.Context()

	resolver := auth.NewResolver(pool, auth.SignupPolicy{Mode: auth.SignupOpen})
	user, err := resolver.Resolve(ctx, auth.Identity{
		Issuer: "https://issuer.test", Subject: "entropy", Email: "entropy@example.com", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("resolve user: %v", err)
	}
	grant := queries.AddUserScopeParams{
		UserID: user.ID, EntityType: queries.EntityTypeSystem, EntityID: uuid.Nil, Scope: queries.ScopeRead,
	}
	if grantErr := q.AddUserScope(ctx, grant); grantErr != nil {
		t.Fatalf("grant scope: %v", grantErr)
	}

	access, refresh, err := machine.IssueSession(ctx, user.ID, nil, "", "")
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	assertBearerFormat(t, access, "loco_s_")
	assertBearerFormat(t, refresh, "loco_r_")

	entity := queries.Entity{Type: queries.EntityTypeUser, ID: user.ID}
	systemRead := queries.EntityScope{
		EntityType: queries.EntityTypeSystem,
		EntityID:   uuid.Nil,
		Scope:      queries.ScopeRead,
	}
	scopes := []queries.EntityScope{systemRead}
	apiToken, err := machine.Issue(ctx, "entropy", user.ID.String(), entity, scopes, time.Hour)
	if err != nil {
		t.Fatalf("issue api token: %v", err)
	}
	assertBearerFormat(t, apiToken, "loco_k_")

	var sessionRows int
	row := pool.QueryRow(ctx,
		"SELECT count(*) FROM session_tokens WHERE access_token_hash = $1 AND refresh_token_hash = $2",
		digestOf(access), digestOf(refresh))
	if scanErr := row.Scan(&sessionRows); scanErr != nil || sessionRows != 1 {
		t.Fatalf("session hashes stored = %d, err %v", sessionRows, scanErr)
	}
	var apiRows int
	row = pool.QueryRow(ctx, "SELECT count(*) FROM api_tokens WHERE token_hash = $1", digestOf(apiToken))
	if scanErr := row.Scan(&apiRows); scanErr != nil || apiRows != 1 {
		t.Fatalf("api hash stored = %d, err %v", apiRows, scanErr)
	}
	var plaintextRows int
	row = pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM session_tokens WHERE access_token_hash = $1 OR refresh_token_hash = $2) +
(SELECT count(*) FROM api_tokens WHERE token_hash = $3)`, access, refresh, apiToken)
	if scanErr := row.Scan(&plaintextRows); scanErr != nil || plaintextRows != 0 {
		t.Fatalf("plaintext tokens stored = %d, err %v", plaintextRows, scanErr)
	}

	sessionEntity, _, err := machine.GetToken(ctx, access)
	if err != nil || sessionEntity.ID != user.ID {
		t.Fatalf("session lookup = %v, %v", sessionEntity, err)
	}
	apiEntity, _, err := machine.GetToken(ctx, apiToken)
	if err != nil || apiEntity.ID != user.ID {
		t.Fatalf("api token lookup = %v, %v", apiEntity, err)
	}
	newAccess, newRefresh, err := machine.Refresh(ctx, refresh)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	assertBearerFormat(t, newAccess, "loco_s_")
	assertBearerFormat(t, newRefresh, "loco_r_")
}
