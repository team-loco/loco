package resource

import (
	"os"

	"github.com/charmbracelet/x/term"
)

func stdoutIsTerminal() bool {
	fd := os.Stdout.Fd()
	return term.IsTerminal(fd)
}
