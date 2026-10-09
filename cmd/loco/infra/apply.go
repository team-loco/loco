package infra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/internal/ui"
)

const applyTimeout = 2 * time.Minute

var (
	errApplyNeedsYes = errors.New("stdout is not a terminal; pass --yes to apply without a prompt")
	errApplyRefused  = errors.New("apply refused")
)

type confirmations struct {
	yes         bool
	destructive bool
	importOps   bool
}

func buildApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply [path]",
		Short: "Apply loco.yaml to an environment",
		Long: `Plan a loco.yaml file against an environment, show the plan, and apply it after a
confirmation. The apply fails when the environment changed since the plan was shown.

Destructive operations need --confirm-destructive and imports need --confirm-import.
Without a terminal, --yes replaces the prompt.

Examples:
  loco infra apply
  loco infra apply --env staging --yes
  loco infra apply --yes --confirm-destructive`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runApply(cmd, locoFilePath(args))
		},
	}
	addTargetFlags(cmd)
	cmd.Flags().BoolP("yes", "y", false, "Apply without asking for confirmation")
	cmd.Flags().Bool("confirm-destructive", false, "Allow the destructive operations of the plan")
	cmd.Flags().Bool("confirm-import", false, "Allow the import operations of the plan")
	return cmd
}

func readConfirmations(cmd *cobra.Command) (confirmations, error) {
	yes, err := cmd.Flags().GetBool("yes")
	if err != nil {
		return confirmations{}, fmt.Errorf("error reading yes flag: %w", err)
	}
	destructive, err := cmd.Flags().GetBool("confirm-destructive")
	if err != nil {
		return confirmations{}, fmt.Errorf("error reading confirm-destructive flag: %w", err)
	}
	importOps, err := cmd.Flags().GetBool("confirm-import")
	if err != nil {
		return confirmations{}, fmt.Errorf("error reading confirm-import flag: %w", err)
	}
	return confirmations{yes: yes, destructive: destructive, importOps: importOps}, nil
}

func runApply(cmd *cobra.Command, path string) error {
	confirm, err := readConfirmations(cmd)
	if err != nil {
		return err
	}
	file, err := readLocoFile(path)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	t, err := resolveTarget(ctx, cmd)
	if err != nil {
		return err
	}
	plan, err := requestPlan(ctx, t, file)
	if err != nil {
		return err
	}
	if len(plan.GetErrors()) > 0 {
		return planErrorsError(path, plan.GetErrors())
	}
	if writeErr := writePlan(os.Stdout, path, plan); writeErr != nil {
		return writeErr
	}
	if len(plan.GetOperations()) == 0 {
		return nil
	}
	unconfirmed := unconfirmedOperations(plan.GetOperations(), confirm)
	if len(unconfirmed) > 0 {
		return refusalError(&planv1.ApplyRefusal{Unconfirmed: unconfirmed}, plan.GetRevision())
	}
	if !confirm.yes {
		proceed, askErr := askToApply()
		if askErr != nil {
			return askErr
		}
		if !proceed {
			fmt.Println("Apply canceled.")
			return nil
		}
	}

	applied, err := requestApply(ctx, t, file, plan.GetRevision(), confirm)
	if err != nil {
		return err
	}
	return writeApplied(os.Stdout, applied)
}

func askToApply() (bool, error) {
	if !cmdutil.StdoutIsTerminal() {
		return false, errApplyNeedsYes
	}
	proceed, err := ui.AskYesNo("Apply these changes?")
	if err != nil {
		return false, fmt.Errorf("failed to prompt user: %w", err)
	}
	return proceed, nil
}

func unconfirmedOperations(operations []*planv1.PlanOperation, confirm confirmations) []*planv1.PlanOperation {
	var unconfirmed []*planv1.PlanOperation
	for _, op := range operations {
		if op.GetDestructive() && !confirm.destructive {
			unconfirmed = append(unconfirmed, op)
			continue
		}
		if op.GetKind() == planv1.PlanOperationKind_PLAN_OPERATION_KIND_IMPORT && !confirm.importOps {
			unconfirmed = append(unconfirmed, op)
		}
	}
	return unconfirmed
}

func requestApply(
	ctx context.Context,
	t target,
	file []byte,
	revision int64,
	confirm confirmations,
) (*planv1.ApplyResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()

	req := connect.NewRequest(&planv1.ApplyRequest{
		File:               file,
		EnvironmentId:      t.environmentID,
		Revision:           revision,
		ConfirmDestructive: confirm.destructive,
		ConfirmImport:      confirm.importOps,
	})
	req.Header().Set("Authorization", t.authHeader)
	resp, err := t.planClient().Apply(ctx, req)
	if err != nil {
		if refusal := applyRefusal(err); refusal != nil {
			return nil, refusalError(refusal, revision)
		}
		return nil, fmt.Errorf("apply: %w", err)
	}
	return resp.Msg, nil
}

func applyRefusal(err error) *planv1.ApplyRefusal {
	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok || connectErr.Code() != connect.CodeFailedPrecondition {
		return nil
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}
		if refusal, isRefusal := value.(*planv1.ApplyRefusal); isRefusal {
			return refusal
		}
	}
	return nil
}

func refusalError(refusal *planv1.ApplyRefusal, plannedRevision int64) error {
	if len(refusal.GetErrors()) > 0 {
		text := formatPlanErrors(refusal.GetErrors())
		return fmt.Errorf("%w: the plan has errors:\n%s", errApplyRefused, indent(text))
	}
	if len(refusal.GetUnconfirmed()) > 0 {
		lines := make([]string, 0, len(refusal.GetUnconfirmed()))
		for _, op := range refusal.GetUnconfirmed() {
			lines = append(lines, op.GetService()+" "+missingConfirmation(op))
		}
		return fmt.Errorf("%w: confirmation missing:\n%s", errApplyRefused, indent(strings.Join(lines, "\n")))
	}
	return fmt.Errorf(
		"%w: the environment changed since the plan (revision %d, now %d); run the command again",
		errApplyRefused, plannedRevision, refusal.GetRevision(),
	)
}

func missingConfirmation(op *planv1.PlanOperation) string {
	if op.GetDestructive() {
		return "is destructive and needs --confirm-destructive"
	}
	return "is an import and needs --confirm-import"
}

func writeApplied(w io.Writer, applied *planv1.ApplyResponse) error {
	var b strings.Builder
	done := lipgloss.NewStyle().Foreground(ui.Ok).Bold(true).Render("Applied.")
	fmt.Fprintf(&b, "\n%s Environment revision is now %d.\n", done, applied.GetRevision())
	for _, deployment := range applied.GetDeployments() {
		fmt.Fprintf(&b, "Started deployment %s for %s in %s.\n",
			deployment.GetDeploymentId(), deployment.GetService(), deployment.GetRegion())
	}
	_, err := lipgloss.Fprint(w, b.String())
	return err
}
