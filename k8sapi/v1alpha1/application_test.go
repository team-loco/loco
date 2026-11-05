package v1alpha1

import (
	"testing"
)

func TestApplicationName(t *testing.T) {
	if got := ApplicationName("abc"); got != "resource-abc" {
		t.Fatalf("ApplicationName = %q", got)
	}
}

func TestApplicationPlacement(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		id          string
		revision    int64
		ok          bool
	}{
		{"both", map[string]string{KeyPlacementID: "p1", KeyPlacementRevision: "4"}, "p1", 4, true},
		{"no id", map[string]string{KeyPlacementRevision: "4"}, "", 0, false},
		{"no revision", map[string]string{KeyPlacementID: "p1"}, "", 0, false},
		{
			"bad revision",
			map[string]string{KeyPlacementID: "p1", KeyPlacementRevision: "x"},
			"",
			0,
			false,
		},
		{"none", nil, "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &Application{}
			app.Annotations = tt.annotations
			id, revision, ok := app.Placement()
			if id != tt.id || revision != tt.revision || ok != tt.ok {
				t.Fatalf("Placement() = %q, %d, %v; want %q, %d, %v", id, revision, ok, tt.id, tt.revision, tt.ok)
			}
		})
	}
}
