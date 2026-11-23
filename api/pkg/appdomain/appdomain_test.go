package appdomain

import "testing"

func TestMatchPicksTheLongestSuffix(t *testing.T) {
	appDomains := []string{"loco.test", "stage.loco.test"}
	matched, label, err := Match("api.stage.loco.test", appDomains)
	if err != nil || matched != "stage.loco.test" || label != "api" {
		t.Fatalf("match = %q %q %v, want api under stage.loco.test", matched, label, err)
	}
	matched, label, err = Match("api.loco.test", appDomains)
	if err != nil || matched != "loco.test" || label != "api" {
		t.Fatalf("match = %q %q %v, want api under loco.test", matched, label, err)
	}
}
