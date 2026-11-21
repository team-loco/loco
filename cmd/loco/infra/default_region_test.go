package infra

import (
	"errors"
	"testing"

	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestDefaultRegion(t *testing.T) {
	t.Parallel()
	const west, east = "us-ca-1", "us-va-1"
	available := resourcev1.RegionAvailability_REGION_AVAILABILITY_AVAILABLE
	offline := resourcev1.RegionAvailability_REGION_AVAILABILITY_OFFLINE
	two := []*resourcev1.RegionInfo{
		{Region: west, Availability: available},
		{Region: east, Availability: available},
	}
	one := []*resourcev1.RegionInfo{
		{Region: west, Availability: offline},
		{Region: east, Availability: available},
	}
	cases := []struct {
		name       string
		configured string
		regions    []*resourcev1.RegionInfo
		want       string
	}{
		{"configured region the environment lists", east, two, east},
		{"configured region the environment lists while offline", west, one, west},
		{"unset with one available region", "", one, east},
		{"configured region the environment does not list", "de-he-1", one, east},
		{"unset with two available regions", "", two, ""},
		{"unset with no regions", "", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := defaultRegion(tc.configured, tc.regions)
			if tc.want == "" {
				if !errors.Is(err, errNoDefaultRegion) {
					t.Fatalf("defaultRegion = %q, %v; want %v", got, err, errNoDefaultRegion)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("defaultRegion = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
