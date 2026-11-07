package main

import "runtime/debug"

const unknownVersion = "(devel)"

var version string

// buildVersion is duplicated in builder/version.go, which is a separate module; keep both in sync.
func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return unknownVersion
	}
	return info.Main.Version
}
