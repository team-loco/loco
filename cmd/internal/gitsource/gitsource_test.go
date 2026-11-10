package gitsource

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	t.Run("outside a repository", func(t *testing.T) {
		got := read(t, t.TempDir())
		if got != (Commit{}) {
			t.Fatalf("Read = %+v, want no commit", got)
		}
	})

	t.Run("repository without commits", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init", "-q", "-b", "main")
		if got := read(t, dir); got != (Commit{}) {
			t.Fatalf("Read = %+v, want no commit", got)
		}
	})

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "web", "main.go"), "package main\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "Fix the health check\n\nThe body is not recorded.")
	sha := runGit(t, dir, "rev-parse", "HEAD")
	subdir := filepath.Join(dir, "web")

	t.Run("clean checkout from a subdirectory", func(t *testing.T) {
		want := Commit{SHA: sha, Branch: "main", Message: "Fix the health check"}
		if got := read(t, subdir); got != want {
			t.Fatalf("Read = %+v, want %+v", got, want)
		}
	})

	t.Run("git not installed", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if got := read(t, dir); got != (Commit{}) {
			t.Fatalf("Read = %+v, want no commit", got)
		}
	})

	t.Run("detached HEAD", func(t *testing.T) {
		runGit(t, dir, "checkout", "-q", "--detach")
		defer runGit(t, dir, "checkout", "-q", "main")
		want := Commit{SHA: sha, Message: "Fix the health check"}
		if got := read(t, dir); got != want {
			t.Fatalf("Read = %+v, want %+v", got, want)
		}
	})
}

func read(t *testing.T, dir string) Commit {
	t.Helper()
	commit, err := Read(t.Context(), dir)
	if err != nil {
		t.Fatalf("Read(%s): %v", dir, err)
	}
	return commit
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Loco Test", "-c", "user.email=test@loco.test", "-C", dir}, args...)
	cmd := exec.CommandContext(t.Context(), "git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
