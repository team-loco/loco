package infra

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
)

const (
	initConfigTimeout = 5 * time.Second
	initFileMode      = 0o644
)

var (
	errConfigExists    = errors.New(locofile.FileName + " already exists. Use --force to overwrite")
	errNoDefaultRegion = errors.New("the API lists no default region")
	errNoSchemaURL     = errors.New("the API reports no schema URL")
)

func buildInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new Loco project",
		Long:  "Create a starter " + locofile.FileName + " in the current directory with one service.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return initCmdFunc(cmd)
		},
	}
	cmd.Flags().BoolP("force", "f", false, "Force overwrite of an existing "+locofile.FileName)
	cmd.Flags().StringP("name", "n", "", "Service name (skips interactive prompt)")
	cmd.Flags().String("host", "", "API host URL")
	return cmd
}

func initCmdFunc(cmd *cobra.Command) error {
	force, err := cmd.Flags().GetBool("force")
	if err != nil {
		return fmt.Errorf("error reading force flag: %w", err)
	}
	appName, err := cmd.Flags().GetString("name")
	if err != nil {
		return fmt.Errorf("error reading name flag: %w", err)
	}

	if _, statErr := os.Stat(locofile.FileName); statErr == nil && !force {
		if appName != "" {
			return errConfigExists
		}
		overwrite, askErr := ui.AskYesNo("A " + locofile.FileName + " already exists. Do you want to overwrite it?")
		if askErr != nil {
			return fmt.Errorf("failed to prompt user: %w", askErr)
		}
		if !overwrite {
			fmt.Println("Aborted.")
			return nil
		}
	}

	if appName == "" {
		var askErr error
		appName, askErr = ui.AskForString("Enter the name of your service (press Enter to use directory name): ")
		if askErr != nil {
			return fmt.Errorf("failed to read service name: %w", askErr)
		}
	}

	if appName == "" {
		workingDir, getwdErr := os.Getwd()
		if getwdErr != nil {
			return fmt.Errorf("failed to get working directory: %w", getwdErr)
		}
		_, dirName := filepath.Split(workingDir)
		appName = dirName
	}

	apiConfig, err := fetchConfig(cmd)
	if err != nil {
		return err
	}
	if apiConfig.GetSchemaUrl() == "" {
		return errNoSchemaURL
	}
	defaults := apiConfig.GetServiceDefaults()
	appDomain := platformDomain(defaults)
	region, err := fetchDefaultRegion(cmd)
	if err != nil {
		return err
	}

	starter := locofile.Starter{
		SchemaURL:   apiConfig.GetSchemaUrl(),
		Name:        appName,
		Hostname:    appName + "." + appDomain,
		Port:        defaults.GetRouting().GetPort(),
		Region:      region,
		CPU:         defaults.GetCpu(),
		Memory:      defaults.GetMemory(),
		MinReplicas: defaults.GetMinReplicas(),
		MaxReplicas: defaults.GetMaxReplicas(),
	}
	data, err := starter.Render()
	if err != nil {
		return err
	}
	if err := os.WriteFile(locofile.FileName, data, initFileMode); err != nil {
		return fmt.Errorf("failed to write %s: %w", locofile.FileName, err)
	}

	style := lipgloss.NewStyle().Foreground(ui.Ok).Bold(true)
	fmt.Printf("Created %s in the current directory.\n", style.Render(locofile.FileName))
	fmt.Printf("Edit the file and run %s to validate your configuration.\n",
		style.Render("loco infra validate"))

	return nil
}

func fetchConfig(cmd *cobra.Command) (*configv1.GetConfigResponse, error) {
	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), initConfigTimeout)
	defer cancel()

	configClient := configv1connect.NewConfigServiceClient(httputil.NewHTTPClient(), host)
	resp, err := configClient.GetConfig(ctx, connect.NewRequest(&configv1.GetConfigRequest{}))
	if err != nil {
		return nil, fmt.Errorf("could not fetch service defaults from the API: %w", err)
	}
	return resp.Msg, nil
}

func fetchDefaultRegion(cmd *cobra.Command) (string, error) {
	token, err := cmdutil.GetCurrentLocoToken(cmd)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), initConfigTimeout)
	defer cancel()

	resourceClient := resourcev1connect.NewResourceServiceClient(httputil.NewHTTPClient(), token.Host)
	req := connect.NewRequest(&resourcev1.ListRegionsRequest{})
	req.Header().Set("Authorization", "Bearer "+token.Token)
	resp, err := resourceClient.ListRegions(ctx, req)
	if err != nil {
		return "", fmt.Errorf("could not list regions from the API: %w", err)
	}
	for _, region := range resp.Msg.GetRegions() {
		if region.GetIsDefault() {
			return region.GetRegion(), nil
		}
	}
	return "", errNoDefaultRegion
}

func platformDomain(defaults *configv1.DefaultServiceConfig) string {
	if cfg, err := session.Load(); err == nil && cfg.DefaultAppDomain != "" {
		return cfg.DefaultAppDomain
	}
	if domain := defaults.GetPlatformDomain(); domain != "" {
		return domain
	}
	return config.DefaultAppDomain
}
