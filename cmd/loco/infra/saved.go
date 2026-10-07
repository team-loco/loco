package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	"github.com/team-loco/loco/gen/go/loco/infra/v1/infrav1connect"
	"github.com/team-loco/loco/internal/httputil"
	definition "github.com/team-loco/loco/internal/infra"
	loco "github.com/team-loco/loco/sdk/go"
	"google.golang.org/protobuf/encoding/protojson"
)

func applySavedPlan(cmd *cobra.Command, path string) error {
	if err := linkedDefaults(cmd); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read saved plan: %w", err)
	}
	var artifact planArtifact
	if decodeErr := loco.Decode(bytes.NewReader(data), &artifact); decodeErr != nil {
		return fmt.Errorf("decode saved plan: %w", decodeErr)
	}
	if artifact.Version != 1 {
		return fmt.Errorf("unsupported saved plan version")
	}
	plan := &infrav1.Plan{}
	if unmarshalErr := protojson.Unmarshal(artifact.Plan, plan); unmarshalErr != nil {
		return fmt.Errorf("decode reviewed operations: %w", unmarshalErr)
	}
	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		return err
	}
	if host != artifact.Host {
		return fmt.Errorf("saved plan targets a different API host; select that host explicitly")
	}
	file, err := cmd.Flags().GetString("file")
	if err != nil {
		return err
	}
	root, err := cmd.Flags().GetString("project-root")
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	module, err := definition.Discover(cwd, file, root)
	if err != nil {
		return err
	}
	digest, err := definition.SourceDigest(cmd.Context(), module.ProjectRoot, artifact.ExcludedPaths, artifact.Reviewed)
	if err != nil {
		return err
	}
	if digest != plan.GetSourceDigest() {
		return fmt.Errorf("source inputs changed after planning; create and review a new plan")
	}
	token, err := cmdutil.GetCurrentLocoToken(cmd)
	if err != nil {
		return err
	}
	selected := &Target{
		Host: host, Token: token.Token, WorkspaceID: plan.GetWorkspaceId(), EnvironmentID: plan.GetEnvironmentId(),
		Client: infrav1connect.NewInfrastructureServiceClient(httputil.NewHTTPClient(), host),
	}
	for _, item := range []struct{ flag, expected string }{
		{workspaceFlag, plan.GetWorkspaceId()}, {environmentFlag, plan.GetEnvironmentId()}, {"stack", plan.GetStackName()},
	} {
		value, getStringErr := cmd.Flags().GetString(item.flag)
		if getStringErr != nil {
			return getStringErr
		}
		if value != "" && value != item.expected {
			return fmt.Errorf("saved plan differs from the selected %s", item.flag)
		}
	}
	printPlan(cmd, plan)
	return applyPlan(cmd, selected, plan)
}

func waitDeployments(cmd *cobra.Command, selected *Target, ids []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
	defer cancel()
	api := deploymentv1connect.NewDeploymentServiceClient(httputil.NewHTTPClient(), selected.Host)
	for _, id := range ids {
		request := connect.NewRequest(&deploymentv1.WatchDeploymentRequest{DeploymentId: id})
		request.Header().Set("Authorization", "Bearer "+selected.Token)
		stream, err := api.WatchDeployment(ctx, request)
		if err != nil {
			return fmt.Errorf("watch deployment %s: %w", id, err)
		}
		ready := false
		for stream.Receive() {
			event := stream.Msg()
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", id, event.GetMessage())
			switch event.GetStatus() {
			case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING:
				ready = true
			case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_SUCCEEDED:
				ready = true
			case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED:
				return fmt.Errorf("deployment %s failed", id)
			case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_CANCELED:
				return fmt.Errorf("deployment %s was canceled", id)
			}
		}
		if errErr := stream.Err(); errErr != nil {
			return fmt.Errorf("watch deployment %s: %w", id, errErr)
		}
		if !ready {
			return fmt.Errorf("deployment %s stopped reporting before readiness", id)
		}
	}
	return nil
}

func newContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "context", Short: "Read sanitized authoring context for isolated evaluation",
		RunE: func(cmd *cobra.Command, _ []string) error {
			selected, err := ResolveTarget(cmd)
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			selected.AuthorContext.ProjectRoot = cwd
			return json.NewEncoder(cmd.OutOrStdout()).Encode(selected.AuthorContext)
		},
	}
	targetFlags(cmd)
	return cmd
}

func BuildSecretCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "secret", Short: "Manage environment-scoped encrypted secrets"}
	set := &cobra.Command{
		Use: "set <name>", Short: "Create an immutable secret version from stdin", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			selected, err := ResolveTarget(cmd)
			if err != nil {
				return err
			}
			value, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 65537))
			if err != nil {
				return err
			}
			if len(value) > 65536 {
				return fmt.Errorf("secret exceeds 64 KiB")
			}
			request := connect.NewRequest(&infrav1.SetSecretRequest{
				EnvironmentId: selected.EnvironmentID, Name: args[0], Value: value,
			})
			request.Header().Set("Authorization", "Bearer "+selected.Token)
			response, err := selected.Client.SetSecret(cmd.Context(), request)
			if err != nil {
				return fmt.Errorf("set secret: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created secret version %s.\n", response.Msg.GetVersionId())
			return nil
		},
	}
	targetFlags(set)
	cmd.AddCommand(set)
	return cmd
}

func newPullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "pull", Short: "Export a managed stack as editable Go",
		RunE: func(cmd *cobra.Command, _ []string) error {
			selected, err := ResolveTarget(cmd)
			if err != nil {
				return err
			}
			name, err := cmd.Flags().GetString("stack")
			if err != nil {
				return err
			}
			if name == "" {
				return fmt.Errorf("--stack is required")
			}
			request := connect.NewRequest(&infrav1.GetStackRequest{
				WorkspaceId: selected.WorkspaceID, EnvironmentId: selected.EnvironmentID, Name: name,
			})
			request.Header().Set("Authorization", "Bearer "+selected.Token)
			response, err := selected.Client.GetStack(cmd.Context(), request)
			if err != nil {
				return err
			}
			manifest, err := definition.AuthorManifest(response.Msg.GetManifest())
			if err != nil {
				return err
			}
			force, err := cmd.Flags().GetBool("force")
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if writeAuthoringErr := definition.WriteAuthoring(cwd, manifest, force); writeAuthoringErr != nil {
				return writeAuthoringErr
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Wrote .loco/main.go.")
			return nil
		},
	}
	targetFlags(cmd)
	cmd.Flags().Bool("force", false, "Overwrite the existing Go definition")
	return cmd
}
