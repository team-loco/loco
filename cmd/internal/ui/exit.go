package ui

import (
	"errors"
	"io"
	"os"
	"strconv"

	"github.com/charmbracelet/x/term"
)

const (
	ExitFailure = 1
	ExitChanges = 2
)

// ExitError ends the command with the given status and no message.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return "exit status " + strconv.Itoa(e.Code)
}

// ExitCode returns the status the process exits with for err.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*ExitError](err); ok {
		return exitErr.Code
	}
	return ExitFailure
}

// IsInteractive reports whether both stdin and stdout are attached to a terminal.
func IsInteractive() bool {
	stdin := os.Stdin.Fd()
	return term.IsTerminal(stdin) && StdoutIsTerminal()
}

// StdoutIsTerminal reports whether stdout is attached to a terminal.
func StdoutIsTerminal() bool {
	fd := os.Stdout.Fd()
	return term.IsTerminal(fd)
}

// TerminalWidth reports the output terminal's width, or zero for redirected output.
func TerminalWidth(out io.Writer) int {
	file, ok := out.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return 0
	}
	width, _, err := term.GetSize(file.Fd())
	if err != nil {
		return 0
	}
	return width
}
