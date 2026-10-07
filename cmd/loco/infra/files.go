package infra

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"
	definition "github.com/team-loco/loco/internal/infra"
)

func artifactDefinition(cmd *cobra.Command) (*definition.Definition, error) {
	file, err := cmd.Flags().GetString("file")
	if err != nil {
		return nil, err
	}
	root, err := cmd.Flags().GetString("project-root")
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return definition.Discover(cwd, file, root)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
