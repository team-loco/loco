package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

func TestOperatorBuildContextExcludesDestinationCredentials(t *testing.T) {
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
