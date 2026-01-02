package loco

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared/config"
)

func init() {
	initCmd.Flags().BoolP("force", "f", false, "Force overwrite of existing loco.toml file")
	initCmd.Flags().StringP("name", "n", "", "Application name (skips interactive prompt)")
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

	if err := config.CreateDefault(appName); err != nil {
		return fmt.Errorf("failed to create loco.toml: %w", err)
	}

	style := lipgloss.NewStyle().Foreground(ui.LocoLightGreen).Bold(true)
	fmt.Printf("Created %s in the current directory.\n", style.Render("loco.toml"))
	fmt.Printf("Edit the file and run %s to validate your configuration.\n",
		style.Render("loco validate"))

	return nil
}
