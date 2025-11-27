package loco

import (
	"fmt"
	"log/slog"

	"github.com/charmbracelet/lipgloss"
	"github.com/loco-team/loco/internal/ui"
	"github.com/loco-team/loco/shared/config"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a loco.toml configuration file",
	Long: `Validate a loco.toml file and catch most configuration errors before deployment.

Note: CPU and memory limits are validated against the Kubernetes resource format.
See https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/ for details.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return validateCmdFunc(cmd)
	},
}

func validateCmdFunc(cmd *cobra.Command) error {
	configPath, err := parseLocoTomlPath(cmd)
	if err != nil {
		return fmt.Errorf("error reading config flag: %w", err)
	}

	loadedCfg, err := config.Load(configPath)
	if err != nil {
		slog.Debug("failed to load config", "path", configPath, "error", err)
		return fmt.Errorf("failed to load loco.toml: %w", err)
	}

	if err := config.Validate(loadedCfg.Config); err != nil {
		slog.Debug("invalid configuration", "error", err)
		return fmt.Errorf("invalid configuration: %w", err)
	}

	style := lipgloss.NewStyle().Foreground(ui.LocoLightGreen).Bold(true)
	fmt.Printf("\n%s loco.toml is valid!\n\n", style.Render("✓"))

	fmt.Printf("Configuration loaded from: %s\n", loadedCfg.ProjectPath)
	fmt.Printf("Application name: %s\n", loadedCfg.Config.Metadata.Name)
	fmt.Printf("Subdomain: %s\n", loadedCfg.Config.Routing.Subdomain)
	fmt.Printf("Port: %d\n", loadedCfg.Config.Routing.Port)

	return nil
}

func init() {
	validateCmd.Flags().StringP("config", "c", "", "path to loco.toml config file (defaults to ./loco.toml)")
}
