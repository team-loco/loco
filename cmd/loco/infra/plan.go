package infra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/internal/locofile"
	"google.golang.org/protobuf/encoding/protojson"
)

const planTimeout = 30 * time.Second

var errPlanHasErrors = errors.New("the plan has errors")

func buildPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan [path]",
		Short: "Show what apply would change in an environment",
		Long: `Send a loco.yaml file to the API and print the operations an apply would perform,
without changing anything.

The exit status is 0 when the plan has no changes and 1 on an error. With
--detailed-exit-code it is 2 when the plan has changes.

Examples:
  loco infra plan
  loco infra plan --env staging
  loco infra plan deploy/loco.yaml --json --detailed-exit-code`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPlan(cmd, locoFilePath(args))
		},
	}
	addTargetFlags(cmd)
	cmd.Flags().Bool("json", false, "Print the plan as JSON")
	cmd.Flags().Bool("detailed-exit-code", false, "Exit with status 2 when the plan has changes")
	return cmd
}

func locoFilePath(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return locofile.FileName
}

func runPlan(cmd *cobra.Command, path string) error {
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return fmt.Errorf("error reading json flag: %w", err)
	}
	detailedExitCode, err := cmd.Flags().GetBool("detailed-exit-code")
	if err != nil {
		return fmt.Errorf("error reading detailed-exit-code flag: %w", err)
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

	if asJSON {
		if writeErr := writeJSON(os.Stdout, plan); writeErr != nil {
			return writeErr
		}
		if len(plan.GetErrors()) > 0 {
			return &cmdutil.ExitError{Code: cmdutil.ExitFailure}
		}
	} else {
		if len(plan.GetErrors()) > 0 {
			return planErrorsError(path, plan.GetErrors())
		}
		if writeErr := writePlan(os.Stdout, path, plan); writeErr != nil {
			return writeErr
		}
	}

	if detailedExitCode && len(plan.GetOperations()) > 0 {
		return &cmdutil.ExitError{Code: cmdutil.ExitChanges}
	}
	return nil
}

func requestPlan(ctx context.Context, t target, file []byte) (*planv1.PlanResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, planTimeout)
	defer cancel()

	req := connect.NewRequest(&planv1.PlanRequest{File: file, EnvironmentId: t.environmentID})
	req.Header().Set("Authorization", t.authHeader)
	resp, err := t.planClient().Plan(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	return resp.Msg, nil
}

func writeJSON(w io.Writer, plan *planv1.PlanResponse) error {
	marshaler := protojson.MarshalOptions{Multiline: true}
	data, err := marshaler.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

func planErrorsError(path string, planErrors []*planv1.PlanError) error {
	text := formatPlanErrors(planErrors)
	return fmt.Errorf("%s cannot be applied:\n%s\n%w", path, indent(text), errPlanHasErrors)
}

func writePlan(w io.Writer, path string, plan *planv1.PlanResponse) error {
	text := formatPlan(path, plan)
	_, err := lipgloss.Fprint(w, text)
	return err
}
