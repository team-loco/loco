package appdomain

import (
	"errors"
	"strings"
	"testing"
)

const (
	locoTest = "loco.test"
	apiLabel = "api"
)

func TestMatchPicksTheLongestSuffix(t *testing.T) {
	appDomains := []string{locoTest, "stage.loco.test"}
	matched, label, err := Match("api.stage.loco.test", appDomains)
	if err != nil || matched != "stage.loco.test" || label != apiLabel {
		t.Fatalf("match = %q %q %v, want api under stage.loco.test", matched, label, err)
	}
	matched, label, err = Match("api.loco.test", appDomains)
	if err != nil || matched != locoTest || label != apiLabel {
		t.Fatalf("match = %q %q %v, want api under loco.test", matched, label, err)
	}
}

func TestMatchRejectsLabelsThatAreNotDNSLabels(t *testing.T) {
	appDomains := []string{locoTest}
	for _, label := range []string{"Store_Front", "Api", "-api", "api-", "a b", strings.Repeat("a", 64)} {
		_, _, err := Match(label+"."+locoTest, appDomains)
		if !errors.Is(err, ErrInvalidLabel) {
			t.Errorf("Match(%q) err = %v, want ErrInvalidLabel", label, err)
		}
	}
	for _, label := range []string{"a", apiLabel, "my-app-2", "1app", strings.Repeat("a", 63)} {
		if _, _, err := Match(label+"."+locoTest, appDomains); err != nil {
			t.Errorf("Match(%q) err = %v, want nil", label, err)
		}
	}
}
