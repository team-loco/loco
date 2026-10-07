package loco

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/infra"
	sdk "github.com/team-loco/loco/sdk/go"
	"google.golang.org/protobuf/encoding/protojson"
)

func newValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a Go infrastructure definition",
		Long:  "Compile and evaluate the Go infrastructure module in .loco/ without deploying resources.",
		RunE:  validateCmdFunc,
	}
	cmd.Flags().String("file", "", "Go infrastructure entrypoint (defaults to nearest .loco/main.go)")
	cmd.Flags().String("project-root", "", "Root for application source paths")
	cmd.Flags().String("environment", "development", "Environment name for offline evaluation")
	cmd.Flags().String("environment-type", "dev", "Environment type for offline evaluation")
	cmd.Flags().String("workspace", "", "Workspace name for offline evaluation")
	cmd.Flags().Bool("json", false, "Write the normalized manifest as JSON")
	return cmd
}

func validateCmdFunc(cmd *cobra.Command, _ []string) error {
	file, err := cmd.Flags().GetString("file")
	if err != nil {
		return err
	}
	root, err := cmd.Flags().GetString("project-root")
	if err != nil {
		return err
	}
	environment, err := cmd.Flags().GetString("environment")
	if err != nil {
		return err
	}
	environmentType, err := cmd.Flags().GetString("environment-type")
	if err != nil {
		return err
	}
	workspace, err := cmd.Flags().GetString("workspace")
	if err != nil {
		return err
	}
	outputJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	definition, err := infra.Discover(cwd, file, root)
	if err != nil {
		return err
	}
	manifest, err := (infra.Evaluator{}).Evaluate(cmd.Context(), definition, &sdk.Context{
		Workspace: workspace, Environment: environment, EnvironmentType: environmentType,
	})
	if err != nil {
		return err
	}
	if outputJSON {
		data, marshalErr := protojson.Marshal(manifest)
		if marshalErr != nil {
			return marshalErr
		}
		if _, err := cmd.OutOrStdout().Write(append(data, '\n')); err != nil {
			return fmt.Errorf("write manifest: %w", err)
		}
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Go infrastructure is valid.")
	fmt.Fprintf(cmd.OutOrStdout(), "Stack: %s\nEnvironment: %s\nServices: %d\n",
		manifest.Stack.Name, environment, len(manifest.Stack.Services))
	return nil
}
