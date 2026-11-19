package locofile

import "strings"

// Slug turns text, such as a directory name, into a DNS label: lowercase letters and digits,
// runs of anything else collapsed to one hyphen, at most 63 characters. It returns "" when the
// text has no letter or digit.
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
