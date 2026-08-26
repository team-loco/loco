package loco

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
)

func init() {
	initCmd.Flags().BoolP("force", "f", false, "Force overwrite of existing loco.toml file")
	initCmd.Flags().StringP("name", "n", "", "Application name (skips interactive prompt)")
	initCmd.Flags().String("host", "", "API host URL")
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new Loco project",
	Long:  "Create a new loco.toml configuration file in the current directory.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return initCmdFunc(cmd)
	},
}

func initCmdFunc(cmd *cobra.Command) error {
	force, err := cmd.Flags().GetBool("force")
	if err != nil {
		return fmt.Errorf("error reading force flag: %w", err)
	}
	// todo: below code is very ugly.
	appName, err := cmd.Flags().GetString("name")
	if err != nil {
		return fmt.Errorf("error reading name flag: %w", err)
	}

	if _, statErr := os.Stat("loco.toml"); statErr == nil && !force {
		if appName == "" {
			overwrite, askErr := ui.AskYesNo("A loco.toml file already exists. Do you want to overwrite it?")
			if askErr != nil {
				return fmt.Errorf("failed to prompt user: %w", askErr)
			}
			if !overwrite {
				fmt.Println("Aborted.")
				return nil
			}
		} else {
			return fmt.Errorf("loco.toml already exists. Use --force to overwrite")
		}
	}

	if appName == "" {
		var askErr error
		appName, askErr = ui.AskForString("Enter the name of your application (press Enter to use directory name): ")
		if askErr != nil {
			return fmt.Errorf("failed to read app name: %w", askErr)
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

	appDomain := fetchPlatformDomain(cmd)

	if err := config.CreateDefault(appName, appDomain); err != nil {
		return fmt.Errorf("failed to create loco.toml: %w", err)
	}

	style := lipgloss.NewStyle().Foreground(ui.LocoLightGreen).Bold(true)
	fmt.Printf("Created %s in the current directory.\n", style.Render("loco.toml"))
	fmt.Printf("Edit the file and run %s to validate your configuration.\n",
		style.Render("loco validate"))

	return nil
}

// fetchPlatformDomain retrieves the default platform domain from the API.
// Falls back to the session config value, then the built-in constant.
func fetchPlatformDomain(cmd *cobra.Command) string {
	// User's explicit session preference takes priority.
	if cfg, err := session.Load(); err == nil && cfg.DefaultAppDomain != "" {
		return cfg.DefaultAppDomain
	}

	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		slog.Debug("could not resolve host for config lookup", "error", err)
		return config.DefaultAppDomain
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	configClient := configv1connect.NewConfigServiceClient(httputil.NewHTTPClient(), host)
	resp, err := configClient.GetDefaultServiceConfig(ctx, connect.NewRequest(&configv1.GetDefaultServiceConfigRequest{}))
	if err != nil {
		slog.Debug("could not fetch defaults from API, using built-in default", "error", err)
		return config.DefaultAppDomain
	}

	if domain := resp.Msg.GetConfig().GetPlatformDomain(); domain != "" {
		return domain
	}

	return config.DefaultAppDomain
}
