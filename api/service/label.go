package service

import (
	"strings"
	"unicode/utf8"
)

const maxLabelLength = 63

// label trims name and reports whether it can name a resource or an environment: 1 to 63
// characters.
func label(name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	return trimmed, trimmed != "" && utf8.RuneCountInString(trimmed) <= maxLabelLength
}
