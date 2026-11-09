package bearer

import (
	"crypto/sha256"
	"encoding/hex"
)

// Hash returns the SHA-256 hex digest of token, the form in which tokens are stored and compared.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
