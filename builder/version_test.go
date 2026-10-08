package main

import (
	"runtime/debug"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	const linked = "sha-0123456789abcdef0123456789abcdef01234567"
	previous := version
	t.Cleanup(func() { version = previous })

	version = linked
	if got := buildVersion(); got != linked {
		t.Errorf("buildVersion() with a linked version = %q, want %q", got, linked)
	}

	version = ""
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("test binary has no build info")
	}
	if got := buildVersion(); got != info.Main.Version {
		t.Errorf("buildVersion() without a linked version = %q, want the module version %q", got, info.Main.Version)
	}
}
