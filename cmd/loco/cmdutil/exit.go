package cmdutil

import (
	"errors"
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

// StdoutIsTerminal reports whether stdout is attached to a terminal.
func StdoutIsTerminal() bool {
	fd := os.Stdout.Fd()
	return term.IsTerminal(fd)
}
