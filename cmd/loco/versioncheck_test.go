package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	develVersion   = "(devel)"
	invalidTag     = "latest"
	currentVersion = "v0.0.53"
	newerVersion   = "v0.0.60"
)

func TestUpgradeHint(t *testing.T) {
	const old = "v0.0.50"
	const minimum = "v0.0.61"
	tests := []struct {
		name     string
		current  string
		minimum  string
		wantHint bool
	}{
		{name: "older patch", current: old, minimum: minimum, wantHint: true},
		{name: "older minor", current: "v0.9.0", minimum: "v0.10.0", wantHint: true},
		{name: "equal", current: minimum, minimum: minimum},
		{name: "newer", current: "v0.0.62", minimum: minimum},
		{name: "no minimum", current: old, minimum: ""},
		{name: "invalid minimum", current: old, minimum: invalidTag},
		{name: "devel build", current: develVersion, minimum: minimum},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := upgradeHint(tt.current, tt.minimum)
			if got := hint != ""; got != tt.wantHint {
				t.Fatalf("upgradeHint(%q, %q) = %q, want hint: %v", tt.current, tt.minimum, hint, tt.wantHint)
			}
		})
	}
}

func TestNewerReleaseHint(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    string
	}{
		{
			name:    "older",
			current: currentVersion,
			latest:  newerVersion,
			want:    "A new version of loco is available: " + currentVersion + " -> " + newerVersion,
		},
		{name: "equal", current: currentVersion, latest: currentVersion},
		{name: "newer", current: "v0.0.61", latest: newerVersion},
		{name: "devel current", current: develVersion, latest: newerVersion},
		{name: "invalid latest", current: currentVersion, latest: invalidTag},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newerReleaseHint(tt.current, tt.latest); got != tt.want {
				t.Fatalf("newerReleaseHint(%q, %q) = %q, want %q", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestReportNewerRelease(t *testing.T) {
	srv, _ := newReleaseServer(t, binaryChecksum())
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	unreachable := httptest.NewServer(http.NotFoundHandler())
	unreachable.Close()

	tests := []struct {
		name    string
		server  *httptest.Server
		current string
		want    []string
	}{
		{
			name:    "older",
			server:  srv,
			current: currentVersion,
			want: []string{
				"A new version of loco is available: " + currentVersion + " -> " + latestTag,
				"Run loco update",
				"Changelog: " + srv.URL + "/releases/tag/" + latestTag,
			},
		},
		{name: "up to date", server: srv, current: latestTag},
		{name: "devel skips lookup", server: srv, current: develVersion},
		{name: "server error", server: failing, current: currentVersion},
		{name: "unreachable", server: unreachable, current: currentVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			reportNewerRelease(context.Background(), &out, newTestUpdater(tt.server), tt.current)
			if len(tt.want) == 0 && out.Len() != 0 {
				t.Fatalf("output = %q, want none", out.String())
			}
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("output = %q, want it to contain %q", out.String(), want)
				}
			}
		})
	}
}

func TestVersionFlagSet(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "long", args: []string{"--version"}, want: true},
		{name: "short", args: []string{"-v"}, want: true},
		{name: "help", args: []string{"--help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCmd(NewEnv())
			root.Version = latestTag
			root.SetArgs(tt.args)
			root.SetOut(&bytes.Buffer{})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if got := versionFlagSet(root); got != tt.want {
				t.Fatalf("versionFlagSet after %v = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
