package platformdomain

import "testing"

func TestMatchPicksTheLongestSuffix(t *testing.T) {
	platformDomains := []string{"loco.test", "stage.loco.test"}
	platform, label, err := Match("api.stage.loco.test", platformDomains)
	if err != nil || platform != "stage.loco.test" || label != "api" {
		t.Fatalf("match = %q %q %v, want api under stage.loco.test", platform, label, err)
	}
	platform, label, err = Match("api.loco.test", platformDomains)
	if err != nil || platform != "loco.test" || label != "api" {
		t.Fatalf("match = %q %q %v, want api under loco.test", platform, label, err)
	}
}
