package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	workerValues = "charts/worker/values.yaml"
	workerChart  = "worker"
)

func TestBumpCharts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		paths     []string
		preBumped bool
	}{
		{name: "digest", paths: []string{workerValues}},
		{name: "tag", paths: []string{workerValues}},
		{name: "grouped", paths: []string{
			workerValues, workerValues, "charts/control/values.yaml", "go.mod",
		}},
		{name: "chart dependency and image", paths: []string{
			"charts/worker/Chart.yaml", workerValues,
		}, preBumped: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			git := func(args ...string) {
				t.Helper()
				cmd := exec.CommandContext(t.Context(), "git", args...)
				cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
					"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			write := func(path, text string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			const original = "apiVersion: v2\nname: test\nversion: 0.3.9\n# keep this comment\n"
			for _, chart := range []string{workerChart, "control"} {
				dir := filepath.Join("charts", chart)
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				write(filepath.Join(dir, "Chart.yaml"), original)
				write(filepath.Join(dir, "values.yaml"), "image: {tag: v1, digest: sha256:aaa}\n")
			}
			git("init", "-q")
			git("add", "charts")
			git("commit", "-qm", "fixture")
			switch tc.name {
			case "digest":
				write(workerValues, "image: {tag: v1, digest: sha256:bbb}\n")
			default:
				write(workerValues, "image: {tag: v2, digest: sha256:bbb}\n")
			}
			if tc.preBumped {
				write("charts/worker/Chart.yaml", strings.Replace(original, "0.3.9", "0.3.10", 1))
			}
			for range 2 {
				if err := bumpCharts(t.Context(), tc.paths); err != nil {
					t.Fatal(err)
				}
			}
			for _, chart := range []string{workerChart, "control"} {
				want := original
				if chart == workerChart || tc.name == "grouped" {
					want = strings.Replace(original, "0.3.9", "0.3.10", 1)
				}
				got, err := os.ReadFile(filepath.Join("charts", chart, "Chart.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != want {
					t.Errorf("%s chart = %q, want %q", chart, got, want)
				}
			}
		})
	}
}
