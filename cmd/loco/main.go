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
	resolved := buildinfo.Version(version)
	Cli(resolved)
}

func Cli(version string) {
	ui.DetectBackground()
	env := NewEnv()
	ctx := context.Background()
	root := NewRootCmd(env)
	err := fang.Execute(ctx,
		root,
		fang.WithVersion(version),
		fang.WithColorSchemeFunc(climain.ColorScheme()),
		fang.WithErrorHandler(climain.HandleError))
	env.versionCheck.report(os.Stderr)
	if err == nil && versionFlagSet(root) {
		releases := newUpdater(releasesURL, latestReleaseTimeout)
		reportNewerRelease(ctx, os.Stderr, releases, root.Version)
	}
	if err != nil {
		os.Exit(cmdutil.ExitCode(err))
	}
}
