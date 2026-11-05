package infra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/ui"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	applyTimeout            = 2 * time.Minute
	confirmDestructiveUsage = "Approve the plan's destructive operations: deleting a service the file no longer " +
		"declares, removing a region where a service runs, or stopping a service the file disables in this environment"
	confirmImportUsage = "Approve the plan's import operations: existing services that no loco.yaml owns yet " +
		"and that this file takes over"
)

var errApplyRefused = errors.New("apply refused")

type applyRequest struct {
	target    cmdutil.Target
	file      []byte
	revision  int64
	images    map[string]string
	confirm   confirmations
	builds    map[string]string
	provision []string
}

type confirmations struct {
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

A new service whose only plan errors are the secrets it declares is created without being
deployed; the command prints the loco env set command for its secrets and exits with an
error, and the next apply deploys it.

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
	cmd.Flags().Bool("confirm-destructive", false, confirmDestructiveUsage)
	cmd.Flags().Bool("confirm-import", false, confirmImportUsage)
	return cmd
}

func readConfirmations(cmd *cobra.Command) (confirmations, error) {
	destructive, err := cmd.Flags().GetBool("confirm-destructive")
	if err != nil {
		return confirmations{}, fmt.Errorf("error reading confirm-destructive flag: %w", err)
	}
	importOps, err := cmd.Flags().GetBool("confirm-import")
	if err != nil {
		return confirmations{}, fmt.Errorf("error reading confirm-import flag: %w", err)
	}
	return confirmations{destructive: destructive, importOps: importOps}, nil
}

func runApply(cmd *cobra.Command, path string) error {
	confirm, err := readConfirmations(cmd)
	if err != nil {
		return err
	}
	file, data, err := loadLocoFile(path)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	t, err := resolveTarget(ctx, cmd)
	if err != nil {
		return err
	}
	environment := t.Environment.GetName()
	resolve := func() (map[string]locofile.Service, error) { return resolveServices(file, path, environment) }
	plan, err := reviewPlan(ctx, cmd, t, path, data, resolve, confirm)
	if err != nil {
		return err
	}
	if len(plan.GetOperations()) == 0 {
		return nil
	}
	if proceed, askErr := cmdutil.Confirm(cmd, "Apply these changes?"); !proceed {
		return askErr
	}

	applied, err := requestApply(ctx, applyRequest{
		target:   t,
		file:     data,
		revision: plan.GetRevision(),
		images:   plan.GetImages(),
		confirm:  confirm,
	})
	if err != nil {
		return err
	}
	return writeApplied(os.Stdout, applied)
}

func resolveServices(file *locofile.File, path, environment string) (map[string]locofile.Service, error) {
	services, err := locofile.Resolve(file, environment)
	if err != nil {
		return nil, fmt.Errorf("%s does not resolve for environment %s: %w", path, environment, err)
	}
	return services, nil
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

func requestApply(ctx context.Context, apply applyRequest) (*planv1.ApplyResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()

	req := &planv1.ApplyRequest{
		File:               apply.file,
		EnvironmentId:      apply.target.Environment.GetId(),
		Revision:           apply.revision,
		ConfirmDestructive: apply.confirm.destructive,
		ConfirmImport:      apply.confirm.importOps,
		Images:             apply.images,
		Builds:             apply.builds,
		Provision:          apply.provision,
	}

	resp, err := apply.target.Clients.Plan.Apply(ctx, req)
	if err != nil {
		connectErr, isConnect := errors.AsType[*connect.Error](err)
		if isConnect && connectErr.Code() == connect.CodeFailedPrecondition {
			if refusal, ok := rpc.ErrorDetail[*planv1.ApplyRefusal](err); ok {
				return nil, refusalError(refusal, apply.revision)
			}
			return nil, fmt.Errorf("%w: %s", errApplyRefused, connectErr.Message())
		}
		return nil, fmt.Errorf("apply: %w", err)
	}
	return resp, nil
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
	if len(refusal.GetImages()) > 0 {
		lines := make([]string, 0, len(refusal.GetImages()))
		for _, reference := range slices.Sorted(maps.Keys(refusal.GetImages())) {
			lines = append(lines, reference+" now resolves to "+refusal.GetImages()[reference])
		}
		return fmt.Errorf(
			"%w: an image tag moved since the plan; run the command again:\n%s",
			errApplyRefused, indent(strings.Join(lines, "\n")),
		)
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
