package tvm

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/team-loco/loco/internal/bearer"
)

const (
	prefixSession = "loco_s_"
	prefixRefresh = "loco_r_"
	prefixAPIKey  = "loco_k_"
)

func generateToken(prefix string) (token string, hash string) {
	token = bearer.New(prefix)
	return token, hashToken(token)
}

// hashToken returns the SHA-256 hex digest of a token string.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
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
