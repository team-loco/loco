package tvm

import (
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
	return token, bearer.Hash(token)
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
