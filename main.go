package main

import (
	"github.com/team-loco/loco/cmd/loco"
	"github.com/team-loco/loco/internal/buildinfo"
)

var version string

func main() {
	resolved := buildinfo.Version(version)
	loco.Cli(resolved)
}
