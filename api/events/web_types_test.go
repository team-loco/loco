package events

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// notInDashboard are the event types the dashboard's event list leaves out: they describe users, system
// admins, CLI sign-ins and clusters, which are not events of an organization or workspace.
var notInDashboard = []string{
	"user.created", "user.updated", "user.deleted", "user.suspended", "user.reinstated",
	"system_admin.claimed", "system_admin.granted", "system_admin.revoked",
	"cli_login.approved", "cli_login.completed",
	"cluster.registered",
}

// TestDashboardListsEveryEventType keeps the dashboard's event list, which feeds the webhook event picker
// and the audit filters, in step with the event types this package records.
func TestDashboardListsEveryEventType(t *testing.T) {
	source, err := os.ReadFile("events.go")
	if err != nil {
		t.Fatal(err)
	}
	constants, _, _ := strings.Cut(string(source), "const (\n\tretentionSpan")
	recorded := regexp.MustCompile(`= "([a-z_]+\.[a-z_]+)"`).FindAllStringSubmatch(constants, -1)

	web, err := os.ReadFile("../../web/src/pages/settings/auditEvents.ts")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\t"([a-z_]+\.[a-z_]+)":`).FindAllStringSubmatch(string(web), -1) {
		listed[m[1]] = true
	}

	for _, m := range recorded {
		eventType := m[1]
		if !listed[eventType] && !slices.Contains(notInDashboard, eventType) {
			t.Errorf("%s is recorded but missing from AUDIT_TYPES in "+
				"web/src/pages/settings/auditEvents.ts; add it there, or to notInDashboard", eventType)
		}
		delete(listed, eventType)
	}
	for eventType := range listed {
		t.Errorf("%s is in AUDIT_TYPES but no constant in events.go records it", eventType)
	}
}
