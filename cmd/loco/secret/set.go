package secret

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/dotenv"
)

var (
	errSeveralStdinNames = errors.New("only one name can take its value from stdin")
)

func newSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set KEY=VALUE [KEY=VALUE...]",
		Short: "Set an environment's secrets by name",
		Long: `Set one or more secrets of an environment. A name without "=" takes its value from stdin,
which keeps it out of the shell history.

The command lists the services that read a changed value; each gets it on its next deploy or
restart.

Examples:
  loco secret set --env production API_KEY=abc123 LOG_LEVEL=debug
  printf '%s' "$DATABASE_URL" | loco secret set --env production DATABASE_URL`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := parseAssignments(cmd.InOrStdin(), args)
			if err != nil {
				return err
			}
			return setSecrets(cmd.Context(), cmd, values)
		},
	}
	addTargetFlags(cmd)
	return cmd
}

func parseAssignments(stdin io.Reader, args []string) (map[string]string, error) {
	values := make(map[string]string, len(args))
	fromStdin := ""
	for i, arg := range args {
		name, value, found := strings.Cut(arg, "=")
		if name == "" {
			return nil, fmt.Errorf("argument %d: %w", i+1, dotenv.ErrEmptyName)
		}
		if !dotenv.ValidName(name) {
			return nil, fmt.Errorf("argument %d: %q: %w", i+1, name, dotenv.ErrInvalidName)
		}
		if found {
			values[name] = value
			continue
		}
		if fromStdin != "" {
			return nil, errSeveralStdinNames
		}
		fromStdin = name
	}
	if fromStdin == "" {
		return values, nil
	}
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("read %s from stdin: %w", fromStdin, err)
	}
	values[fromStdin] = strings.TrimSuffix(string(raw), "\n")
	return values, nil
}
