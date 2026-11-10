package clip_test

import (
	"testing"
	"unicode/utf8"

	"github.com/team-loco/loco/internal/clip"
)

const (
	short   = "abc"
	euroEnd = "ab€"
)

func TestBytes(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"shorter than the limit", short, 5, short},
		{"exactly the limit", short, 3, short},
		{"ascii over the limit", "abcdef", 3, "abc"},
		{"rune ends at the limit", euroEnd, 5, euroEnd},
		{"rune straddles the limit", euroEnd, 4, "ab"},
		{"limit inside the first rune", "€", 2, ""},
		{"zero limit", short, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clip.Bytes(tt.in, tt.limit)
			if got != tt.want {
				t.Fatalf("Bytes(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("Bytes(%q, %d) = %q is not valid UTF-8", tt.in, tt.limit, got)
			}
		})
	}
}

func TestRunes(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"shorter than the limit", short, 5, short},
		{"exactly the limit", euroEnd, 3, euroEnd},
		{"over the limit", euroEnd, 2, "ab"},
		{"multi-byte runes", "€€€", 2, "€€"},
		{"zero limit", short, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clip.Runes(tt.in, tt.limit); got != tt.want {
				t.Fatalf("Runes(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}
		})
	}
}
