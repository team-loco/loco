package locofile

import (
	"strings"
	"unicode/utf8"
)

// Slug turns a name into a DNS label: lowercase letters and digits, runs of anything else
// collapsed to one hyphen, at most 63 characters. It returns "" when the name has no letter or
// digit. A resource's private hostname and generated subdomain are the slug of its name.
func Slug(name string) string {
	var b strings.Builder
	for _, char := range strings.ToLower(name) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			b.WriteRune(char)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
		if b.Len() >= MaxServiceNameLength {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// IsServiceName reports whether name can name a service: at most 63 characters, with a letter or
// digit for its slug.
func IsServiceName(name string) bool {
	return utf8.RuneCountInString(name) <= MaxServiceNameLength && Slug(name) != ""
}
