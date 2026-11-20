package locofile

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Slug turns text, such as a name or a directory name, into a DNS label: accents dropped,
// lowercase letters and digits kept, runs of anything else collapsed to one hyphen, at most 63
// characters. It returns "" when the text has no letter or digit.
func Slug(name string) string {
	var b strings.Builder
	for _, char := range norm.NFD.String(strings.ToLower(name)) {
		switch {
		case unicode.Is(unicode.Mn, char):
			continue
		case char >= 'a' && char <= 'z' || char >= '0' && char <= '9':
			b.WriteRune(char)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
		if b.Len() >= MaxServiceNameLength {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
