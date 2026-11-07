package main

import (
	"context"
	"os"
	"syscall"

	"charm.land/fang/v2"
	"github.com/team-loco/loco/cmd/internal/ui"
	"github.com/team-loco/loco/internal/buildinfo"
)

var version string

func main() {
	ui.DetectBackground()
	err := fang.Execute(context.Background(),
		NewRootCmd(),
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
		fang.WithVersion(buildinfo.Version(version)),
		fang.WithColorSchemeFunc(ui.ColorScheme()),
		fang.WithErrorHandler(ui.HandleError))
	if err != nil {
		os.Exit(ui.ExitCode(err))
	}
}
