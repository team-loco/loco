package infra

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	definition "github.com/team-loco/loco/internal/infra"
)

const (
	workspaceFlag   = "workspace"
	environmentFlag = "environment"
)

type linkedTarget struct {
	Version       int    `json:"version"`
	Host          string `json:"host"`
	WorkspaceID   string `json:"workspaceId"`
	EnvironmentID string `json:"environmentId"`
	Stack         string `json:"stack,omitempty"`
}

func linkedDefaults(cmd *cobra.Command) error {
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		file := filepath.Join(directory, ".loco", "link.json")
		data, readErr := os.ReadFile(file)
		if readErr == nil {
			var target linkedTarget
			if decodeErr := definition.DecodeJSON(bytes.NewReader(data), &target); decodeErr != nil {
				return fmt.Errorf("read linked target: %w", decodeErr)
			}
			if target.Version != 1 || target.Host == "" || !looksLikeID(target.WorkspaceID) ||
				!looksLikeID(target.EnvironmentID) {
				return fmt.Errorf("invalid linked target")
			}
			values := map[string]string{
				"host":          target.Host,
				workspaceFlag:   target.WorkspaceID,
				environmentFlag: target.EnvironmentID,
				"stack":         target.Stack,
			}
			for name, value := range values {
				flag := cmd.Flags().Lookup(name)
				if flag == nil || flag.Changed || value == "" {
					continue
				}
				environment := linkedEnvironmentVariable(name)
				if environment != "" && os.Getenv(environment) != "" {
					continue
				}
				if setErr := cmd.Flags().Set(name, value); setErr != nil {
					return setErr
				}
			}
			return nil
		}
		if !os.IsNotExist(readErr) {
			return readErr
		}
		if _, gitErr := os.Stat(filepath.Join(directory, ".git")); gitErr == nil {
			return nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return nil
		}
		directory = parent
	}
}

func newLinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Save this project’s workspace and environment target",
		RunE: func(cmd *cobra.Command, _ []string) error {
			selected, err := ResolveTarget(cmd)
			if err != nil {
				return err
			}
			module, err := artifactDefinition(cmd)
			if err != nil {
				return err
			}
			stack, err := cmd.Flags().GetString("stack")
			if err != nil {
				return err
			}
			return writeJSON(
				filepath.Join(module.ModuleRoot, "link.json"),
				linkedTarget{
					Version:       1,
					Host:          selected.Host,
					WorkspaceID:   selected.WorkspaceID,
					EnvironmentID: selected.EnvironmentID,
					Stack:         stack,
				},
			)
		},
	}
	targetFlags(cmd)
	definitionFlags(cmd)
	return cmd
}

func linkedEnvironmentVariable(name string) string {
	switch name {
	case "host":
		return "LOCO_HOST"
	case workspaceFlag:
		return "LOCO_WORKSPACE"
	case environmentFlag:
		return "LOCO_ENVIRONMENT"
	default:
		return ""
	}
}
