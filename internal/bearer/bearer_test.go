package bearer_test

import (
	"encoding/base64"
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
