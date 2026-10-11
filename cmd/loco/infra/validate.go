package infra

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/ui"
)

func buildValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [path]",
		Short: "Validate a loco.yaml file",
		Long: `Parse a loco.yaml file and check its structure against the schema, without calling the API.

Resource limits and defaults are checked by the API when the file is planned or applied.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := locofile.FileName
			if len(args) == 1 {
				path = args[0]
			}
			return validateCmdFunc(path)
		},
	}
}

func validateCmdFunc(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	file, err := locofile.Parse(data)
	if err != nil {
		return formatValidationError(path, data, err)
	}

	style := lipgloss.NewStyle().Foreground(ui.Ok).Bold(true)
	fmt.Printf("%s %s is valid\n", style.Render("✓"), path)
	fmt.Printf("Partial: %s\n", file.Partial)
	fmt.Printf("Services: %s\n", strings.Join(serviceNames(file), ", "))
	return nil
}

func formatValidationError(path string, data []byte, err error) error {
	parseErr, ok := errors.AsType[*locofile.ParseError](err)
	if !ok || parseErr.Line == 0 {
		return fmt.Errorf("invalid %s:\n%s", path, indent(err.Error()))
	}
	lines := strings.Split(string(data), "\n")
	if parseErr.Line > len(lines) {
		return fmt.Errorf("invalid %s at line %d: %w", path, parseErr.Line, err)
	}
	source := lines[parseErr.Line-1]
	return fmt.Errorf("invalid %s at line %d: %w\n  %d | %s", path, parseErr.Line, err, parseErr.Line, source)
}

func indent(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func serviceNames(file *locofile.File) []string {
	names := maps.Keys(file.Services)
	return slices.Sorted(names)
}
