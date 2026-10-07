package infra

import (
	"fmt"
	"net/url"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	registryv1 "github.com/team-loco/loco/gen/go/loco/registry/v1"
	"github.com/team-loco/loco/gen/go/loco/registry/v1/registryv1connect"
	"github.com/team-loco/loco/internal/docker"
	"github.com/team-loco/loco/internal/httputil"
	definition "github.com/team-loco/loco/internal/infra"
	loco "github.com/team-loco/loco/sdk/go"
)

func BuildDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "deploy [service-key]", Short: "Build and deploy Go-defined infrastructure and applications",
		Long: "Build and deploy the stack in .loco/main.go, or select one service without pruning its siblings.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if err := cmd.Flags().Set("service", args[0]); err != nil {
					return err
				}
			}
			selected, plan, err := createPlan(cmd, true)
			if err != nil {
				return err
			}
			planOnly, err := cmd.Flags().GetBool("plan-only")
			if err != nil {
				return err
			}
			if planOnly {
				return nil
			}
			return applyPlan(cmd, selected, plan)
		},
	}
	planFlags(cmd)
	applyFlags(cmd)
	cmd.Flags().Bool("plan-only", false, "Build and save the combined deployment plan without applying it")
	return cmd
}

func buildImages(
	cmd *cobra.Command,
	selected *Target,
	module *definition.Definition,
	manifest *loco.Manifest,
	selector string,
) (map[string]string, error) {
	reviewed, reviewedErr := cmd.Flags().GetBool("reviewed")
	if reviewedErr != nil {
		return nil, reviewedErr
	}
	if reviewed {
		snapshot, cleanup, snapshotErr := definition.Snapshot(cmd.Context(), module)
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		defer cleanup()
		module = snapshot
	}
	bindings := make(map[string]string)
	found := selector == ""
	api := registryv1connect.NewRegistryServiceClient(httputil.NewHTTPClient(), selected.Host)
	host, err := url.Parse(selected.Host)
	if err != nil || host.Host == "" {
		return nil, fmt.Errorf("invalid API host for image publishing")
	}
	for _, service := range manifest.Stack.Services {
		if selector != "" && service.Key != selector {
			continue
		}
		found = true
		if service.GetDocker() == nil && strings.Contains(service.GetImage(), "@sha256:") {
			bindings[service.Key] = service.GetImage()
			continue
		}
		var build *definition.BuildRequest
		if service.GetDocker() != nil {
			build, err = definition.ResolveBuild(module.ProjectRoot, service.GetDocker())
			if err != nil {
				return nil, fmt.Errorf("service %q: %w", service.Key, err)
			}
		}
		image, buildAndPublishErr := buildAndPublish(cmd, selected, api, host.Host, manifest.Stack.Name, service, build)
		if buildAndPublishErr != nil {
			return nil, fmt.Errorf("service %q: %w", service.Key, buildAndPublishErr)
		}
		bindings[service.Key] = image
	}
	if !found {
		return nil, fmt.Errorf("service key %q is not declared", selector)
	}
	return bindings, nil
}

func buildAndPublish(
	cmd *cobra.Command,
	selected *Target,
	api registryv1connect.RegistryServiceClient,
	registryHost, stack string,
	service *loco.Service,
	build *definition.BuildRequest,
) (string, error) {
	request := connect.NewRequest(&registryv1.GetImageRepositoryRequest{
		EnvironmentId: selected.EnvironmentID, StackName: stack, ServiceKey: service.Key,
	})
	request.Header().Set("Authorization", "Bearer "+selected.Token)
	response, err := api.GetImageRepository(cmd.Context(), request)
	if err != nil {
		return "", fmt.Errorf("resolve publishing repository: %w", err)
	}
	expected := "loco/" + selected.EnvironmentID + "/" + stack + "/" + service.Key
	if response.Msg.GetPushRepository() != expected {
		return "", fmt.Errorf("registry returned an unexpected publishing target")
	}
	engine, err := docker.NewClient(build)
	if err != nil {
		return "", err
	}
	defer engine.Close()
	engine.GenerateImageTag(registryHost+"/"+expected, "", selected.WorkspaceID, service.Key)
	engine.SetRegistry(registryHost)
	logf := func(message string) { fmt.Fprintln(cmd.ErrOrStderr(), message) }
	if build != nil {
		if buildImageErr := engine.BuildImage(cmd.Context(), logf); buildImageErr != nil {
			return "", buildImageErr
		}
	} else {
		if pullImageErr := engine.PullImage(cmd.Context(), service.GetImage(), logf); pullImageErr != nil {
			return "", pullImageErr
		}
		if imageTagErr := engine.ImageTag(cmd.Context(), service.GetImage()); imageTagErr != nil {
			return "", imageTagErr
		}
	}
	if validateImageErr := engine.ValidateImage(cmd.Context(), engine.ImageName, logf); validateImageErr != nil {
		return "", validateImageErr
	}
	if pushImageErr := engine.PushImage(cmd.Context(), logf, "loco", selected.Token); pushImageErr != nil {
		return "", pushImageErr
	}
	digest, err := engine.ImageDigest(cmd.Context(), engine.ImageName)
	if err != nil {
		return "", err
	}
	return response.Msg.GetRepository() + "@" + digest, nil
}
