package bearer_test

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/team-loco/loco/internal/bearer"
)

func TestNewEncodesBytesRandomBytesAfterThePrefix(t *testing.T) {
	const prefix = "loco_t_"
	token := bearer.New(prefix)
	encoded, ok := strings.CutPrefix(token, prefix)
	if !ok {
		t.Fatalf("token %q lacks prefix %q", token, prefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != bearer.Bytes {
		t.Fatalf("decoded length = %d, want %d", len(raw), bearer.Bytes)
	}
	if bearer.Bytes*8 < 256 {
		t.Fatalf("Bytes = %d, want at least 256 bits", bearer.Bytes)
	}
}

func TestNewWithoutPrefixIsOnlyTheEncodedBytes(t *testing.T) {
	raw, err := base64.RawURLEncoding.DecodeString(bearer.New(""))
	if err != nil || len(raw) != bearer.Bytes {
		t.Fatalf("decode = %d bytes, err %v", len(raw), err)
	}
}

func TestNewIsUnique(t *testing.T) {
	const count = 1000
	seen := make(map[string]struct{}, count)
	for range count {
		token := bearer.New("")
		if _, dup := seen[token]; dup {
			t.Fatalf("duplicate token %q", token)
		}
		seen[token] = struct{}{}
	}
}

func TestFromHeader(t *testing.T) {
	withAuthorization := func(value string) http.Header {
		h := http.Header{}
		h.Set("Authorization", value)
		return h
	}
	cases := []struct {
		name   string
		header http.Header
		token  string
		ok     bool
	}{
		{"missing", http.Header{}, "", false},
		{"wrong scheme", withAuthorization("Basic abc"), "", false},
		{"empty token", withAuthorization("Bearer "), "", false},
		{"ok", withAuthorization("Bearer abc"), "abc", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token, ok := bearer.FromHeader(tc.header)
			if token != tc.token || ok != tc.ok {
				t.Fatalf("FromHeader = %q, %v; want %q, %v", token, ok, tc.token, tc.ok)
			}
		})
	}
}

func TestHeaderRoundTripsThroughFromHeader(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", bearer.Header("abc"))
	if token, ok := bearer.FromHeader(h); !ok || token != "abc" {
		t.Fatalf("FromHeader = %q, %v", token, ok)
	}
}
