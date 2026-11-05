package builds

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateMessageStartsOnARuneBoundary(t *testing.T) {
	message := "€" + strings.Repeat("a", maxBuildMessageLength-1)

	got := truncateMessage(message)

	if !utf8.ValidString(got) {
		t.Fatalf("truncated message is not valid UTF-8: %q", got)
	}
	if want := "..." + strings.Repeat("a", maxBuildMessageLength-1); got != want {
		t.Fatalf("got %d bytes, want %d", len(got), len(want))
	}
}
