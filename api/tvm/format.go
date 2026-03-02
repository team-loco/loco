package tvm

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	prefixSession = "loco_s_"
	prefixRefresh = "loco_r_"
	prefixAPIKey  = "loco_k_"
)

// generateToken creates a new token with the given prefix using a UUIDv7,
// and returns both the raw token string and its SHA-256 hex hash.
// The raw token is returned to the caller once and never stored.
// Only the hash is persisted.
func generateToken(prefix string) (token string, hash string) {
	id := uuid.Must(uuid.NewV7())
	encoded := base64.RawURLEncoding.EncodeToString(id[:])
	token = prefix + encoded
	return token, hashToken(token)
}

// hashToken returns the SHA-256 hex digest of a token string.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum)
}

// tokenPrefix returns the prefix of the token, or empty string if unrecognized.
func tokenPrefix(token string) string {
	switch {
	case strings.HasPrefix(token, prefixSession):
		return prefixSession
	case strings.HasPrefix(token, prefixRefresh):
		return prefixRefresh
	case strings.HasPrefix(token, prefixAPIKey):
		return prefixAPIKey
	default:
		return ""
	}
}
