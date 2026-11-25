package endpointurl

import "testing"

func TestParse(t *testing.T) {
	for raw, valid := range map[string]bool{
		"http://localhost:8080":           true,
		"https://api.loco.test:8443":      true,
		"https://[::1]:8443":              true,
		"https://api.loco.test/api":       true,
		"https://api.loco.test/":          true,
		"https://api.loco.test":           true,
		"api.loco.test":                   false,
		"api.loco.test:8080":              false,
		"ftp://api.loco.test":             false,
		"HTTPS://api.loco.test":           true,
		"https://":                        false,
		"https://:8443":                   false,
		"https://user:pass@api.loco.test": false,
		"https://user@api.loco.test":      false,
		"https://api.loco.test?x=1":       false,
		"https://api.loco.test?":          false,
		"https://api.loco.test#frag":      false,
		"https://api.loco.test#":          false,
		"https://api.loco.test/%zz":       false,
		"":                                false,
	} {
		t.Run(raw, func(t *testing.T) {
			u, err := Parse(raw)
			if (err == nil) != valid {
				t.Fatalf("Parse(%q) error = %v, want valid %t", raw, err, valid)
			}
			if valid && u == nil {
				t.Fatalf("Parse(%q) returned a nil URL", raw)
			}
		})
	}
}
