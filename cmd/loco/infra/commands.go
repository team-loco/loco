package infra

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	definition "github.com/team-loco/loco/internal/infra"
	"github.com/team-loco/loco/internal/ui"
	loco "github.com/team-loco/loco/sdk/go"
	"google.golang.org/protobuf/encoding/protojson"
)

type planArtifact struct {
	Version       int             `json:"version"`
	Host          string          `json:"host"`
	Reviewed      bool            `json:"reviewed"`
	ExcludedPaths []string        `json:"excludedPaths"`
	Plan          json.RawMessage `json:"plan"`
}

func BuildCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "infra", Short: "Plan and apply Go infrastructure"}
	cmd.AddCommand(
		newPlanCmd(),
		newApplyCmd(),
		newContextCmd(),
		newPullCmd(),
		newLinkCmd(),
	)
	return cmd
}

func planFlags(cmd *cobra.Command) {
	targetFlags(cmd)
	definitionFlags(cmd)
	cmd.Flags().String("manifest", "", "Use a previously evaluated authoring manifest")
	cmd.Flags().String("images", "", "JSON map of service keys to pinned image digests")
	cmd.Flags().String("out", "", "Save the immutable plan artifact")
	cmd.Flags().Bool("reviewed", false, "Require clean tracked Git inputs")
	cmd.Flags().Bool("json", false, "Print the redacted plan as JSON")
	cmd.Flags().Bool("detailed-exit-code", false, "Exit 2 when changes are pending")
	cmd.Flags().String("service", "", "Plan one service without pruning siblings")
}

func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "plan", Short: "Preview infrastructure and deployment changes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, plan, err := createPlan(cmd)
			if err != nil {
				return err
			}
			detailed, err := cmd.Flags().GetBool("detailed-exit-code")
			if err != nil {
				return err
			}
			if detailed && len(plan.GetOperations()) > 0 {
				return &cmdutil.ExitError{Code: 2}
			}
			return nil
		},
	}
	planFlags(cmd)
	return cmd
}

func evaluateManifest(cmd *cobra.Command, selected *Target) (*loco.Manifest, *definition.Definition, error) {
	file, err := cmd.Flags().GetString("file")
	if err != nil {
		return nil, nil, err
	}
	root, err := cmd.Flags().GetString("project-root")
	if err != nil {
		return nil, nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	module, err := definition.Discover(cwd, file, root)
	if err != nil {
		return nil, nil, err
	}
	manifestFile, err := cmd.Flags().GetString("manifest")
	if err != nil {
		return nil, nil, err
	}
	var manifest *loco.Manifest
	if manifestFile == "" {
		manifest, err = (definition.Evaluator{}).Evaluate(cmd.Context(), module, selected.AuthorContext)
	} else {
		data, readErr := os.ReadFile(manifestFile)
		if readErr != nil {
			return nil, nil, readErr
		}
		manifest = &loco.Manifest{}
		err = definition.DecodeJSON(bytes.NewReader(data), manifest)
		if err == nil {
			err = definition.ValidateManifest(manifest)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	stack, err := cmd.Flags().GetString("stack")
	if err != nil {
		return nil, nil, err
	}
	if stack != "" && stack != manifest.Stack.Name {
		return nil, nil, fmt.Errorf("selected stack differs from the Go definition")
	}
	return manifest, module, nil
}

func createPlan(cmd *cobra.Command) (*Target, *infrav1.Plan, error) {
	selected, err := ResolveTarget(cmd)
	if err != nil {
		return nil, nil, err
	}
	manifest, module, err := evaluateManifest(cmd, selected)
	if err != nil {
		return nil, nil, err
	}
	graph, err := definition.ProtoManifest(manifest)
	if err != nil {
		return nil, nil, err
	}
	imagesFile, err := cmd.Flags().GetString("images")
	if err != nil {
		return nil, nil, err
	}
	bindings := make(map[string]string)
	if imagesFile != "" {
		data, readFileErr := os.ReadFile(imagesFile)
		if readFileErr != nil {
			return nil, nil, readFileErr
		}
		if decodeErr := definition.DecodeJSON(bytes.NewReader(data), &bindings); decodeErr != nil {
			return nil, nil, decodeErr
		}
	}
	selector, err := cmd.Flags().GetString("service")
	if err != nil {
		return nil, nil, err
	}
	out, err := cmd.Flags().GetString("out")
	if err != nil {
		return nil, nil, err
	}
	excluded, err := excludedArtifactPaths(module.ProjectRoot, out, imagesFile)
	if err != nil {
		return nil, nil, err
	}
	reviewed, err := cmd.Flags().GetBool("reviewed")
	if err != nil {
		return nil, nil, err
	}
	if err = definition.ValidateSourceDefinition(cmd.Context(), module, reviewed); err != nil {
		return nil, nil, err
	}
	digest, err := definition.SourceDigest(cmd.Context(), module.ProjectRoot, excluded, reviewed)
	if err != nil {
		return nil, nil, err
	}
	if err = bindManifestImages(graph, bindings, selector); err != nil {
		return nil, nil, err
	}
	request := connect.NewRequest(&infrav1.PlanInfrastructureRequest{
		WorkspaceId: selected.WorkspaceID, EnvironmentId: selected.EnvironmentID,
		Manifest: graph, SourceDigest: digest, ServiceKey: selector,
	})
	request.Header().Set("Authorization", "Bearer "+selected.Token)
	response, err := selected.Client.PlanInfrastructure(cmd.Context(), request)
	if err != nil {
		return nil, nil, fmt.Errorf("plan infrastructure: %w", err)
	}
	plan := response.Msg.GetPlan()
	encoded, err := protojson.Marshal(plan)
	if err != nil {
		return nil, nil, err
	}
	artifact := &planArtifact{
		Version: 1, Host: selected.Host, Reviewed: reviewed, ExcludedPaths: excluded, Plan: encoded,
	}
	if out != "" {
		data, marshalIndentErr := json.MarshalIndent(artifact, "", "  ")
		if marshalIndentErr != nil {
			return nil, nil, marshalIndentErr
		}
		if writeFileErr := os.WriteFile(out, append(data, '\n'), 0o600); writeFileErr != nil {
			return nil, nil, fmt.Errorf("save plan: %w", writeFileErr)
		}
	}
	outputJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return nil, nil, err
	}
	if outputJSON {
		fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
	} else {
		printPlan(cmd, plan)
	}
	return selected, plan, nil
}

func printPlan(cmd *cobra.Command, plan *infrav1.Plan) {
	fmt.Fprintf(cmd.OutOrStdout(), "Stack %s · environment %s\n", plan.GetStackName(), plan.GetEnvironmentId())
	if len(plan.GetOperations()) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Infrastructure and deployments are up to date.")
	}
	for _, operation := range plan.GetOperations() {
		marker := "~"
		switch operation.GetType() {
		case infrav1.OperationType_OPERATION_TYPE_CREATE:
			marker = "+"
		case infrav1.OperationType_OPERATION_TYPE_DELETE:
			marker = "-"
		}
		fmt.Fprintf(
			cmd.OutOrStdout(),
			"%s service.%s %v",
			marker,
			operation.GetServiceKey(),
			operation.GetChangedFields(),
		)
		if operation.GetDestructive() {
			fmt.Fprint(cmd.OutOrStdout(), " [destructive]")
		}
		if operation.GetImageDigest() != "" {
			fmt.Fprintf(cmd.OutOrStdout(), " %s", operation.GetImageDigest())
		}
		fmt.Fprintln(cmd.OutOrStdout())
	}
}

func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "apply", Short: "Apply infrastructure intent or an exact saved deployment plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := cmd.Flags().GetString("plan")
			if err != nil {
				return err
			}
			if path != "" {
				return applySavedPlan(cmd, path)
			}
			selected, plan, err := createPlan(cmd)
			if err != nil {
				return err
			}
			return applyPlan(cmd, selected, plan)
		},
	}
	planFlags(cmd)
	applyFlags(cmd)
	cmd.Flags().String("plan", "", "Apply the saved plan without evaluating Go or building images")
	return cmd
}

func applyFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("yes", false, "Apply without an interactive confirmation")
	cmd.Flags().Bool("confirm-destructive", false, "Allow reviewed destructive operations")
	cmd.Flags().Bool("wait", false, "Wait for every scheduled deployment to become ready")
}

func applyPlan(cmd *cobra.Command, selected *Target, plan *infrav1.Plan) error {
	yes, err := cmd.Flags().GetBool("yes")
	if err != nil {
		return err
	}
	if !yes {
		confirmed, askYesNoErr := ui.AskYesNo("Apply this infrastructure plan?")
		if askYesNoErr != nil {
			return askYesNoErr
		}
		if !confirmed {
			return fmt.Errorf("apply canceled")
		}
	}
	destructive, err := cmd.Flags().GetBool("confirm-destructive")
	if err != nil {
		return err
	}
	digest, digestErr := definition.PlanDigest(plan)
	if digestErr != nil {
		return digestErr
	}
	request := connect.NewRequest(&infrav1.ApplyInfrastructureRequest{
		PlanId:             plan.GetId(),
		ManifestDigest:     plan.GetManifestDigest(),
		SourceDigest:       plan.GetSourceDigest(),
		PlanDigest:         digest,
		ConfirmDestructive: destructive,
	})
	request.Header().Set("Authorization", "Bearer "+selected.Token)
	response, err := selected.Client.ApplyInfrastructure(cmd.Context(), request)
	if err != nil {
		return fmt.Errorf("apply infrastructure: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Applied %s; scheduled %d deployments.\n",
		response.Msg.GetApplyId(), len(response.Msg.GetDeploymentIds()))
	wait, err := cmd.Flags().GetBool("wait")
	if err != nil {
		return err
	}
	if wait {
		return waitDeployments(cmd, selected, response.Msg.GetDeploymentIds())
	}
	return nil
}

func excludedArtifactPaths(root string, paths ...string) ([]string, error) {
	excluded := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(root, absolute)
		if err != nil {
			return nil, err
		}
		excluded = append(excluded, filepath.ToSlash(relative))
	}
	return excluded, nil
}

func bindManifestImages(graph *infrav1.StackManifest, bindings map[string]string, selector string) error {
	for _, service := range graph.GetServices() {
		if selector != "" && selector != service.GetKey() {
			continue
		}
		if image, ok := bindings[service.GetKey()]; ok {
			service.ResolvedImage = image
		} else if service.GetImage() != "" {
			if !bytes.Contains([]byte(service.GetImage()), []byte("@sha256:")) {
				return fmt.Errorf(
					"image source for %q must be pinned or supplied in --images",
					service.GetKey(),
				)
			}
			service.ResolvedImage = service.GetImage()
		}
	}
	return nil
}
