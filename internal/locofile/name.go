package locofile

import "strings"

// ServiceNameFromDir turns a directory name into a valid service name: lowercase letters and
// digits, runs of anything else collapsed to one hyphen, at most 63 characters. It returns ""
// when the directory name has no letter or digit.
func ServiceNameFromDir(dir string) string {
	var b strings.Builder
	for _, char := range strings.ToLower(dir) {
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
