package env

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

var (
	errSeveralStdinNames = errors.New("only one name can take its value from stdin")
	errEmptyName         = errors.New("empty name")
)

func newSetCmd(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set KEY=VALUE [KEY=VALUE...]",
		Short: "Set secrets by name",
		Long: `Set one or more secrets. A name without "=" takes its value from stdin, which keeps it out of
the shell history.

Examples:
  loco env set --env production API_KEY=abc123 LOG_LEVEL=debug
  printf '%s' "$DATABASE_URL" | loco env set --env production DATABASE_URL`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := parseAssignments(d.Stdin, args)
			if err != nil {
				return err
			}
			return setSecrets(cmd.Context(), cmd, d, values)
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
			return nil, fmt.Errorf("argument %d: %w", i+1, errEmptyName)
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
