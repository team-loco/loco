package bearer

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
)

const Bytes = 32

const scheme = "Bearer "

// HeaderGetter is satisfied by http.Header and by the connect request header.
type HeaderGetter interface {
	Get(string) string
}

func New(prefix string) string {
	raw := make([]byte, Bytes)
	_, _ = rand.Read(raw)
	return prefix + base64.RawURLEncoding.EncodeToString(raw)
}

// FromHeader returns the token of the request's Authorization header, as Token does.
func FromHeader(h HeaderGetter) (string, bool) {
	return Token(h.Get("Authorization"))
}

// Token returns the token of an Authorization header value of the form "Bearer <token>".
// It reports false when the value uses another scheme or carries an empty token.
func Token(authorization string) (string, bool) {
	token, ok := strings.CutPrefix(authorization, scheme)
	if !ok || token == "" {
		return "", false
	}
	return token, true
}

// Header returns the Authorization header value that presents token.
func Header(token string) string {
	return scheme + token
}
