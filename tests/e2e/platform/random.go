package platform

import (
	"crypto/rand"
	"encoding/hex"
)

// RandomHex is size random bytes in hex.
func RandomHex(size int) string {
	data := make([]byte, size)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}
