// Package clip shortens text to a byte or rune budget without splitting a rune.
package clip

import "unicode/utf8"

// Bytes returns s cut to at most limit bytes, backing up to the last rune
// boundary so the result is valid UTF-8 whenever s is.
func Bytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

// Runes returns s cut to at most limit runes.
func Runes(s string, limit int) string {
	for i := range s {
		if limit == 0 {
			return s[:i]
		}
		limit--
	}
	return s
}
