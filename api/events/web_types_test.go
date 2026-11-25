package events

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// notInDashboard are the recorded types the dashboard leaves out.
var notInDashboard = []string{
	"user.created", "user.updated", "user.deleted", "user.suspended", "user.reinstated",
	"system_role.claimed", "system_role.granted", "system_role.revoked",
	"cli_login.approved", "cli_login.completed",
	"cluster.registered",
}

// TestDashboardListsEveryEventType compares the types events.go records with AUDIT_TYPES in the dashboard.
func TestDashboardListsEveryEventType(t *testing.T) {
	source, err := os.ReadFile("events.go")
	if err != nil {
		t.Fatal(err)
	}
	declared := regexp.MustCompile(`(?m)^\t[A-Z]\w* += ("[a-z_]+\.[a-z_]+")$`)
	matches := declared.FindAllStringSubmatch(string(source), -1)
	recorded := make([]string, 0, len(matches))
	for _, m := range matches {
		recorded = append(recorded, strings.Trim(m[1], `"`))
	}
	if len(recorded) < 50 {
		t.Fatalf("found %d event type constants in events.go; the pattern matches too few", len(recorded))
	}

	web, err := os.ReadFile("../../web/src/pages/settings/auditEvents.ts")
	if err != nil {
		t.Fatal(err)
	}
	_, afterStart, found := strings.Cut(string(web), "const AUDIT_TYPES")
	if !found {
		t.Fatal("AUDIT_TYPES not found in auditEvents.ts")
	}
	literal, _, _ := strings.Cut(afterStart, "\n};")
	listed := map[string]bool{}
	keyed := regexp.MustCompile(`(?m)^\t"([a-z_]+\.[a-z_]+)":`)
	for _, m := range keyed.FindAllStringSubmatch(literal, -1) {
		listed[m[1]] = true
	}
	if len(listed) < 50 {
		t.Fatalf("found %d keys in AUDIT_TYPES; the pattern no longer matches its entries", len(listed))
	}

	for _, eventType := range recorded {
		if !listed[eventType] && !slices.Contains(notInDashboard, eventType) {
			t.Errorf("%s is recorded but not in AUDIT_TYPES; add it there or to notInDashboard", eventType)
		}
		delete(listed, eventType)
	}
	for eventType := range listed {
		t.Errorf("%s is in AUDIT_TYPES but events.go records no such type", eventType)
	}
}
