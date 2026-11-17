package servicedefaults

import "testing"

func TestRollbackOnFailureResolution(t *testing.T) {
	for _, tc := range []struct {
		name       string
		service    bool
		deployment *bool
		want       bool
	}{
		{name: "default"},
		{name: "service true", service: true, want: true},
		{name: "deployment false wins", service: true, deployment: new(false)},
		{name: "deployment true wins", deployment: new(true), want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RollbackOnFailure(tc.service, tc.deployment); got != tc.want {
				t.Fatalf("rollback = %v, want %v", got, tc.want)
			}
		})
	}
}
