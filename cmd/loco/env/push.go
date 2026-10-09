package env

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/dotenv"
)

var errEmptyEnvFile = errors.New("the .env input has no KEY=VALUE lines")

func newPushCmd(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push [file]",
		Short: "Set every secret in a .env file",
		Long: `Read KEY=VALUE lines from a .env file, or from stdin without a file, and set them in one call.

Nothing is expanded: $NAME stays literal.

Examples:
  loco env push --env production .env
  cat .env | loco env push --env production`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := readEnvInput(d.Stdin, args)
			if err != nil {
				return err
			}
			return setSecrets(cmd.Context(), cmd, d, values)
		},
	}
	addTargetFlags(cmd)
	return cmd
}

func readEnvInput(stdin io.Reader, args []string) (map[string]string, error) {
	input := stdin
	source := "stdin"
	if len(args) == 1 {
		source = args[0]
		file, err := os.Open(source)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", source, err)
		}
		defer file.Close()
		input = file
	}
	values, err := dotenv.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if len(values) == 0 {
		return nil, errEmptyEnvFile
	}
	return values, nil
}
