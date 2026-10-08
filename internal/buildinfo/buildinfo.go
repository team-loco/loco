package buildinfo

import "runtime/debug"

const unknownVersion = "(devel)"

func Version(linked string) string {
	if linked != "" {
		return linked
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return unknownVersion
	}
	return info.Main.Version
}
