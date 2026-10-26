package main

import (
	"context"
	"os"

	"charm.land/fang/v2"
	"github.com/team-loco/loco/cmd/internal/ui"
	"github.com/team-loco/loco/cmd/loco/climain"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/buildinfo"
)

var version string

func main() {
	ui.DetectBackground()
	err := fang.Execute(context.Background(),
		NewRootCmd(),
		fang.WithVersion(buildinfo.Version(version)),
		fang.WithColorSchemeFunc(climain.ColorScheme()),
		fang.WithErrorHandler(climain.HandleError))
	if err != nil {
		os.Exit(cmdutil.ExitCode(err))
	}
}
