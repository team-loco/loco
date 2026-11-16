package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
)

func terminalMain() {
	width, err := strconv.ParseUint(os.Args[1], 10, 16)
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "loco", os.Args[2:]...) //nolint:gosec // Arguments come from the test script.
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: uint16(width)})
	if err != nil {
		panic(err)
	}
	defer terminal.Close()
	output, err := io.ReadAll(terminal)
	if err != nil && !errors.Is(err, syscall.EIO) {
		panic(err)
	}
	if err := cmd.Wait(); err != nil {
		panic(err)
	}
	text := strings.ReplaceAll(ansi.Strip(string(output)), "\r", "")
	fmt.Print(text)
	for line := range strings.SplitSeq(text, "\n") {
		if ansi.StringWidth(line) > int(width) {
			fmt.Fprintf(os.Stderr, "line width %d exceeds terminal width %d\n", ansi.StringWidth(line), width)
			os.Exit(1)
		}
	}
}
