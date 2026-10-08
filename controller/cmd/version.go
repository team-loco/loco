package main

import "runtime/debug"

const unknownVersion = "(devel)"

var version string

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
