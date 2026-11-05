package cmdutil

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/ui"
)

var (
	ErrNotInteractive = errors.New("stdin and stdout must be a terminal to prompt")

	isInteractive = IsInteractive
)

// Confirm asks question unless the command's --yes flag is set. It reports false after printing
// "Aborted." when the user declines.
func Confirm(cmd *cobra.Command, question string) (bool, error) {
	yes, err := cmd.Flags().GetBool("yes")
	if err != nil {
		return false, fmt.Errorf("error reading yes flag: %w", err)
	}
	if yes {
		return true, nil
	}
	confirmed, err := AskYesNo(cmd, question)
	if errors.Is(err, ErrNotInteractive) {
		return false, fmt.Errorf("%w; pass --yes to confirm without a prompt", err)
	}
	return confirmed, err
}

// AskYesNo prompts for a yes or no answer on the command's input and output. It reports false
// after printing "Aborted." when the user declines.
func AskYesNo(cmd *cobra.Command, question string) (bool, error) {
	if !isInteractive() {
		return false, ErrNotInteractive
	}
	confirmed, err := ui.AskYesNo(cmd.InOrStdin(), cmd.OutOrStdout(), question)
	if err != nil {
		return false, fmt.Errorf("failed to prompt for confirmation: %w", err)
	}
	if !confirmed {
		fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
	}
	return confirmed, nil
}

// AskString prompts for a line of text on the command's input and output.
func AskString(cmd *cobra.Command, prompt string) (string, error) {
	if !isInteractive() {
		return "", ErrNotInteractive
	}
	value, err := ui.AskForString(cmd.InOrStdin(), cmd.OutOrStdout(), prompt)
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}
	return value, nil
}
