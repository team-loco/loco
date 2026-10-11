package infra

import (
	"bytes"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

func plainPlan(t *testing.T, plan *planv1.PlanResponse) string {
	t.Helper()
	var buf bytes.Buffer
	if _, err := lipgloss.Fprint(&buf, formatPlan("loco.yaml", plan)); err != nil {
		t.Fatalf("lipgloss.Fprint: %v", err)
	}
	return buf.String()
}

func TestFormatPlanWithoutOperations(t *testing.T) {
	got := formatPlan("loco.yaml", &planv1.PlanResponse{Revision: 7})
	want := "No changes. Environment revision 7 matches loco.yaml.\n"
	if got != want {
		t.Fatalf("formatPlan() = %q, want %q", got, want)
	}
}

func TestFormatPlanGroupsOperationsByKind(t *testing.T) {
	plan := &planv1.PlanResponse{
		Revision: 7,
		Operations: []*planv1.PlanOperation{
			{Kind: planv1.PlanOperationKind_PLAN_OPERATION_KIND_DELETE, Service: "worker", Destructive: true},
			{Kind: planv1.PlanOperationKind_PLAN_OPERATION_KIND_IMPORT, Service: "db"},
			{
				Kind:        planv1.PlanOperationKind_PLAN_OPERATION_KIND_UPDATE,
				Service:     "web",
				NeedsDeploy: true,
				Changes: []*planv1.FieldChange{
					{Path: "regions.us-east-1.cpu", Before: "250m", After: "500m"},
					{Path: "env.TRACE", Before: "on"},
				},
			},
			{
				Kind:    planv1.PlanOperationKind_PLAN_OPERATION_KIND_CREATE,
				Service: "api",
				Changes: []*planv1.FieldChange{{Path: "image", After: "ghcr.io/acme/api:1"}},
			},
		},
	}
	got := plainPlan(t, plan)
	wantInOrder := []string{
		"Plan for loco.yaml against environment revision 7.",
		"Create",
		"  + api",
		"      image: ghcr.io/acme/api:1",
		"Update",
		"  ~ web  (needs deploy)",
		"      regions.us-east-1.cpu: 250m -> 500m",
		"      env.TRACE: on -> (removed)",
		"Delete",
		"  - worker  (destructive)",
		"Import",
		"  ← db",
		"Plan: 1 to create, 1 to update, 1 to delete, 1 to import.",
		"Destructive operations need --confirm-destructive on apply.",
		"Import operations need --confirm-import on apply.",
		"Services marked needs deploy start after loco deploy builds them.",
	}
	rest := got
	for _, line := range wantInOrder {
		index := strings.Index(rest, line)
		if index < 0 {
			t.Fatalf("formatPlan() lacks %q after the previous line:\n%s", line, got)
		}
		rest = rest[index+len(line):]
	}
}

func TestFormatPlanErrors(t *testing.T) {
	planErrors := []*planv1.PlanError{
		{Service: "web", Path: "regions.us-west-9", Message: "region us-west-9 is not available"},
		{Service: "db", Message: "service db belongs to partial platform"},
		{Path: "environments", Message: "environment staging does not exist"},
		{Message: "the file declares no services"},
	}
	got := formatPlanErrors(planErrors)
	want := strings.Join([]string{
		"web regions.us-west-9: region us-west-9 is not available",
		"db: service db belongs to partial platform",
		"environments: environment staging does not exist",
		"the file declares no services",
	}, "\n")
	if got != want {
		t.Fatalf("formatPlanErrors() = %q, want %q", got, want)
	}
}
