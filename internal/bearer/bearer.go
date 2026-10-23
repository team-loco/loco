package bearer

import (
	"crypto/rand"
	"encoding/base64"
)

const Bytes = 32

func New(prefix string) string {
	raw := make([]byte, Bytes)
	_, _ = rand.Read(raw)
	return prefix + base64.RawURLEncoding.EncodeToString(raw)
}
