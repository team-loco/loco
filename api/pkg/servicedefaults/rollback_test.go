package servicedefaults

import "testing"

func TestRollbackOnFailureResolution(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		resource, deployment *bool
		want                 bool
	}{
		{name: "default"},
		{name: "resource true", resource: new(true), want: true},
		{name: "resource false", resource: new(false)},
		{name: "deployment false wins", resource: new(true), deployment: new(false)},
		{name: "deployment true wins", resource: new(false), deployment: new(true), want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RollbackOnFailure(tc.resource, tc.deployment); got != tc.want {
				t.Fatalf("rollback = %v, want %v", got, tc.want)
			}
		})
	}
}
