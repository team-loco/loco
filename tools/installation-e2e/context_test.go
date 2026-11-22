package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

func operatorContext(t *testing.T) *patternmatcher.PatternMatcher {
	t.Helper()
	path := filepath.Join("..", "..", "tests", "e2e", "fixtures", "installation", "operator.Dockerfile.dockerignore")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	patterns, err := ignorefile.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	matcher, err := patternmatcher.New(patterns)
	if err != nil {
		t.Fatal(err)
	}
	return matcher
}

func TestOperatorBuildContextExcludesDestinationCredentials(t *testing.T) {
	matcher := operatorContext(t)
	for _, path := range []string{
		"tests/e2e/runs/install/kubeconfig",
		"tests/e2e/runs/install/docker/config.json",
		".git/config", ".env", "dev/api.env", "cmd/.env", "cmd/credentials.json",
	} {
		excluded, matchErr := matcher.MatchesOrParentMatches(path)
		if matchErr != nil {
			t.Fatal(matchErr)
		}
		if !excluded {
			t.Fatalf("credential-bearing context path is included: %s", path)
		}
	}
	for _, path := range []string{
		"cmd/locoadm/main.go",
		"tools/installation-e2e/main.go",
		"tools/internal/devicelogin/login.go",
		"examples/gitops/clusters/control-plane/loco.yaml",
		"tests/e2e/fixtures/build-app/main.go",
		"tests/e2e/fixtures/installation/recovery.yaml",
	} {
		excluded, matchErr := matcher.MatchesOrParentMatches(path)
		if matchErr != nil {
			t.Fatal(matchErr)
		}
		if excluded {
			t.Fatalf("required source path is excluded: %s", path)
		}
	}
}

func TestOperatorBuildContextHoldsEveryRepositoryPackageTheRunnerImports(t *testing.T) {
	matcher := operatorContext(t)
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "-f", "{{.Dir}}", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for dir := range strings.FieldsSeq(string(out)) {
		rel, relErr := filepath.Rel(root, dir)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		excluded, matchErr := matcher.MatchesOrParentMatches(filepath.ToSlash(filepath.Join(rel, "main.go")))
		if matchErr != nil {
			t.Fatal(matchErr)
		}
		if excluded {
			t.Errorf("the operator build context leaves out an imported package: %s", rel)
		}
	}
}
