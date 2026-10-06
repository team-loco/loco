package loco

import "testing"

func TestUpgradeHint(t *testing.T) {
	const old = "v0.0.50"
	const minimum = "v0.0.61"
	tests := []struct {
		name     string
		current  string
		minimum  string
		wantHint bool
	}{
		{name: "older patch", current: old, minimum: minimum, wantHint: true},
		{name: "older minor", current: "v0.9.0", minimum: "v0.10.0", wantHint: true},
		{name: "equal", current: minimum, minimum: minimum},
		{name: "newer", current: "v0.0.62", minimum: minimum},
		{name: "no minimum", current: old, minimum: ""},
		{name: "invalid minimum", current: old, minimum: "latest"},
		{name: "devel build", current: "(devel)", minimum: minimum},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := upgradeHint(tt.current, tt.minimum)
			if got := hint != ""; got != tt.wantHint {
				t.Fatalf("upgradeHint(%q, %q) = %q, want hint: %v", tt.current, tt.minimum, hint, tt.wantHint)
			}
		})
	}
}
