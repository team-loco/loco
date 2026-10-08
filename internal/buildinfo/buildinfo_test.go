package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestVersionPrefersTheLinkedVersion(t *testing.T) {
	const linked = "sha-0123456789abcdef0123456789abcdef01234567"
	if got := Version(linked); got != linked {
		t.Errorf("Version(%q) = %q", linked, got)
	}
}

func TestVersionFallsBackToTheModuleVersion(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("test binary has no build info")
	}
	if got := Version(""); got != info.Main.Version {
		t.Errorf("Version(\"\") = %q, want the main module version %q", got, info.Main.Version)
	}
}
