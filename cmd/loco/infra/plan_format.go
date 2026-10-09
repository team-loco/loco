package infra

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/internal/ui"
)

const (
	markerCreate = "+"
	markerUpdate = "~"
	markerDelete = "-"
	markerImport = "←"
	changeIndent = "      "
)

var kindOrder = []planv1.PlanOperationKind{
	planv1.PlanOperationKind_PLAN_OPERATION_KIND_CREATE,
	planv1.PlanOperationKind_PLAN_OPERATION_KIND_UPDATE,
	planv1.PlanOperationKind_PLAN_OPERATION_KIND_DELETE,
	planv1.PlanOperationKind_PLAN_OPERATION_KIND_IMPORT,
}

type kindStyle struct {
	heading string
	verb    string
	marker  string
	color   ui.Color
}

func styleFor(kind planv1.PlanOperationKind) kindStyle {
	switch kind {
	case planv1.PlanOperationKind_PLAN_OPERATION_KIND_CREATE:
		return kindStyle{heading: "Create", verb: "to create", marker: markerCreate, color: ui.Ok}
	case planv1.PlanOperationKind_PLAN_OPERATION_KIND_UPDATE:
		return kindStyle{heading: "Update", verb: "to update", marker: markerUpdate, color: ui.Warn}
	case planv1.PlanOperationKind_PLAN_OPERATION_KIND_DELETE:
		return kindStyle{heading: "Delete", verb: "to delete", marker: markerDelete, color: ui.Bad}
	case planv1.PlanOperationKind_PLAN_OPERATION_KIND_IMPORT:
		return kindStyle{heading: "Import", verb: "to import", marker: markerImport, color: ui.Info}
	case planv1.PlanOperationKind_PLAN_OPERATION_KIND_UNSPECIFIED:
		return kindStyle{heading: "Unknown", verb: "unknown", marker: "?", color: ui.Fg3}
	default:
		return kindStyle{heading: "Unknown", verb: "unknown", marker: "?", color: ui.Fg3}
	}
}

func formatPlan(path string, plan *planv1.PlanResponse) string {
	operations := plan.GetOperations()
	revision := plan.GetRevision()
	if len(operations) == 0 {
		return fmt.Sprintf("No changes. Environment revision %d matches %s.\n", revision, path)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Plan for %s against environment revision %d.\n", path, revision)
	var summary []string
	for _, kind := range kindOrder {
		ops := operationsOfKind(operations, kind)
		if len(ops) == 0 {
			continue
		}
		style := styleFor(kind)
		heading := lipgloss.NewStyle().Bold(true).Render(style.heading)
		fmt.Fprintf(&b, "\n%s\n", heading)
		for _, op := range ops {
			writeOperation(&b, style, op)
		}
		summary = append(summary, fmt.Sprintf("%d %s", len(ops), style.verb))
	}
	fmt.Fprintf(&b, "\nPlan: %s.\n", strings.Join(summary, ", "))
	if hasDestructive(operations) {
		b.WriteString("Destructive operations need --confirm-destructive on apply.\n")
	}
	if hasKind(operations, planv1.PlanOperationKind_PLAN_OPERATION_KIND_IMPORT) {
		b.WriteString("Import operations need --confirm-import on apply.\n")
	}
	if hasNeedsDeploy(operations) {
		b.WriteString("Services marked needs deploy start after loco deploy builds them.\n")
	}
	return b.String()
}

func writeOperation(b *strings.Builder, style kindStyle, op *planv1.PlanOperation) {
	marker := lipgloss.NewStyle().Foreground(style.color).Bold(true).Render(style.marker)
	line := fmt.Sprintf("  %s %s", marker, op.GetService())
	var notes []string
	if op.GetDestructive() {
		notes = append(notes, "destructive")
	}
	if op.GetNeedsDeploy() {
		notes = append(notes, "needs deploy")
	}
	if len(notes) > 0 {
		note := lipgloss.NewStyle().Foreground(ui.Fg3).Render("(" + strings.Join(notes, ", ") + ")")
		line += "  " + note
	}
	b.WriteString(line + "\n")
	for _, change := range op.GetChanges() {
		b.WriteString(changeIndent + formatChange(change) + "\n")
	}
}

func formatChange(change *planv1.FieldChange) string {
	before := change.GetBefore()
	after := change.GetAfter()
	switch {
	case before == "":
		return fmt.Sprintf("%s: %s", change.GetPath(), after)
	case after == "":
		return fmt.Sprintf("%s: %s -> (removed)", change.GetPath(), before)
	default:
		return fmt.Sprintf("%s: %s -> %s", change.GetPath(), before, after)
	}
}

func formatPlanErrors(planErrors []*planv1.PlanError) string {
	lines := make([]string, 0, len(planErrors))
	for _, planErr := range planErrors {
		subject := strings.TrimSpace(planErr.GetService() + " " + planErr.GetPath())
		if subject == "" {
			lines = append(lines, planErr.GetMessage())
			continue
		}
		lines = append(lines, subject+": "+planErr.GetMessage())
	}
	return strings.Join(lines, "\n")
}

func operationsOfKind(operations []*planv1.PlanOperation, kind planv1.PlanOperationKind) []*planv1.PlanOperation {
	var ops []*planv1.PlanOperation
	for _, op := range operations {
		if op.GetKind() == kind {
			ops = append(ops, op)
		}
	}
	return ops
}

func hasKind(operations []*planv1.PlanOperation, kind planv1.PlanOperationKind) bool {
	return len(operationsOfKind(operations, kind)) > 0
}

func hasDestructive(operations []*planv1.PlanOperation) bool {
	for _, op := range operations {
		if op.GetDestructive() {
			return true
		}
	}
	return false
}

func hasNeedsDeploy(operations []*planv1.PlanOperation) bool {
	for _, op := range operations {
		if op.GetNeedsDeploy() {
			return true
		}
	}
	return false
}
