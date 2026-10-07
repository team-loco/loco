package infra

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitFixture(t *testing.T) *Definition {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".loco", "main.go"), "package main\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, ".loco", "go.mod"), "module fixture\ngo 1.24.0\n")
	writeFile(t, filepath.Join(root, "app.txt"), "reviewed application")
	writeFile(t, filepath.Join(root, ".gitignore"), "ignored.txt\n")
	runGit(t, root, "init", "-q")
	runGit(t, root, "add", "--", ".loco/main.go", ".loco/go.mod", "app.txt", ".gitignore")
	runGit(
		t,
		root,
		"-c",
		"commit.gpgsign=false",
		"-c",
		"core.hooksPath=/dev/null",
		"-c",
		"user.name=Fixture",
		"-c",
		"user.email=fixture@example.com",
		"commit",
		"-qm",
		"fixture",
	)
	module, err := Discover(root, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return module
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Git fixture: %v: %s", err, data)
	}
}

func TestReviewedSnapshotIncludesOnlyTrackedInputs(t *testing.T) {
	module := gitFixture(t)
	writeFile(t, filepath.Join(module.ProjectRoot, "ignored.txt"), "unreviewed input")
	digest, err := SourceDigest(context.Background(), module.ProjectRoot, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, cleanup, err := Snapshot(context.Background(), module)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err = os.Stat(filepath.Join(snapshot.ProjectRoot, "ignored.txt")); !os.IsNotExist(err) {
		t.Fatal("ignored input entered reviewed snapshot")
	}
	data, err := os.ReadFile(filepath.Join(snapshot.ProjectRoot, "app.txt"))
	if err != nil || string(data) != "reviewed application" {
		t.Fatalf("missing tracked application input: %v", err)
	}
	writeFile(t, filepath.Join(module.ProjectRoot, "new.txt"), "untracked input")
	if _, err = SourceDigest(context.Background(), module.ProjectRoot, nil, true); err == nil {
		t.Fatal("reviewed source accepted an untracked input")
	}
	if _, err = SourceDigest(context.Background(), module.ProjectRoot, []string{"new.txt"}, true); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(module.ProjectRoot, "app.txt"), "changed")
	if _, err = SourceDigest(context.Background(), module.ProjectRoot, nil, true); err == nil {
		t.Fatal("reviewed source accepted tracked drift")
	}
	local, err := SourceDigest(context.Background(), module.ProjectRoot, nil, false)
	if err != nil || local == digest {
		t.Fatalf("local application drift was missed: %v", err)
	}
}

func TestLocalModuleReplacementCannotEscapeReviewedSource(t *testing.T) {
	module := gitFixture(t)
	writeFile(
		t,
		filepath.Join(module.ModuleRoot, "go.mod"),
		"module fixture\ngo 1.24.0\nreplace example.com/shared => ../../outside\n",
	)
	if err := ValidateSourceDefinition(context.Background(), module, true); err == nil {
		t.Fatal("external local replacement accepted")
	}
}
