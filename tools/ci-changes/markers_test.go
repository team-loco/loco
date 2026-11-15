package main

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const apiFile = "api/main.go"

const markerConfig = `
api:
  pull_request: [api/**]
  push: [api/**]
web:
  pull_request: [web/**]
publish:
  reuse: false
  pull_request: [api/**]
images:
  for: [api]
  push: ["**"]
`

type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.write(".github/ci-paths.yml", markerConfig)
	files := []string{apiFile, "web/index.ts", "docs/index.md", ".github/workflows/api.yml"}
	for _, path := range append(files, markerInputs...) {
		r.write(path, path)
	}
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	out, err := git(r.t.Context(), r.dir, args...)
	require.NoError(r.t, err)
	return strings.TrimSpace(string(out))
}

// write commits content at path and returns the commit.
func (r *repo) write(path, content string) string {
	r.t.Helper()
	full := filepath.Join(r.dir, path)
	require.NoError(r.t, os.MkdirAll(filepath.Dir(full), 0o700))
	require.NoError(r.t, os.WriteFile(full, []byte(content), 0o600))
	r.git("add", path)
	r.git("-c", "user.name=ci", "-c", "user.email=ci@loco.test", "commit", "-q", "-m", path)
	return r.git("rev-parse", "HEAD")
}

func (r *repo) keys(selected ...string) map[string]string {
	r.t.Helper()
	filters, err := loadFilters(filepath.Join(r.dir, ".github", "ci-paths.yml"))
	require.NoError(r.t, err)
	own := map[string]bool{}
	for _, workflow := range selected {
		own[workflow] = true
	}
	keys, err := markerKeys(r.t.Context(), r.dir, filters, own)
	require.NoError(r.t, err)
	return keys
}

func TestMarkerKeyFollowsOnlyTheWorkflowsInputs(t *testing.T) {
	r := newRepo(t)
	before := r.keys("api", "web")
	require.Len(t, before, 2)
	assert.NotEqual(t, before["api"], before["web"], "the workflow name is part of the key")

	r.write("docs/index.md", "changed")
	r.write("web/index.ts", "changed")
	after := r.keys("api", "web")
	assert.Equal(t, before["api"], after["api"], "files outside the workflow's paths leave its key")
	assert.NotEqual(t, before["web"], after["web"])

	for _, path := range append([]string{apiFile, "api/new.go", ".github/workflows/api.yml"}, markerInputs...) {
		t.Run(path, func(t *testing.T) {
			previous := r.keys("api")["api"]
			r.write(path, "changed "+path)
			assert.NotEqual(t, previous, r.keys("api")["api"])
		})
	}
}

func TestMarkerKeysCoverOnlySelectedReusableWorkflows(t *testing.T) {
	keys := newRepo(t).keys("api", "publish", "images")
	assert.Equal(t, []string{"api"}, slices.Sorted(maps.Keys(keys)),
		"a workflow with reuse: false or a for list always runs")
}

func runSelection(t *testing.T, r *repo, event, base, head string, lookup markerLookup) map[string]string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.WriteFile(output, nil, 0o600))
	t.Chdir(r.dir)
	require.NoError(t, run(t.Context(), ".github/ci-paths.yml", event, base, head, output, lookup))
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	result := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		key, value, _ := strings.Cut(line, "=")
		result[key] = value
	}
	return result
}

func TestAPassForTheSameInputsSkipsTheWorkflowAndWhatOnlyItUses(t *testing.T) {
	r := newRepo(t)
	base := r.git("rev-parse", "HEAD")
	head := r.write(apiFile, "changed")
	apiKey := r.keys("api")["api"]
	var looked []string
	found := func(_ context.Context, key string) (bool, error) {
		looked = append(looked, key)
		return key == apiKey, nil
	}

	selected := runSelection(t, r, "push", base, head, found)
	assert.Equal(t, []string{apiKey}, looked, "only selected reusable workflows are looked up")
	assert.Equal(t, "false", selected["api"], "the marker stands in for the run")
	assert.Equal(t, "true", selected["images"], "images runs on its own push paths")
	assert.Equal(t, "{}", selected["markers"], "a reused pass writes no marker")

	selected = runSelection(t, r, "pull_request", base, head, found)
	assert.Equal(t, "false", selected["api"])
	assert.Equal(t, "false", selected["images"], "nothing that runs needs the images")
	assert.Equal(t, "true", selected["publish"], "reuse: false runs on every match")
}

func TestAWorkflowWithoutAPassRunsAndNamesItsMarker(t *testing.T) {
	r := newRepo(t)
	base := r.git("rev-parse", "HEAD")
	head := r.write(apiFile, "changed")
	apiKey := r.keys("api")["api"]
	for name, lookup := range map[string]markerLookup{
		"no marker":     func(context.Context, string) (bool, error) { return false, nil },
		"lookup failed": func(context.Context, string) (bool, error) { return true, errors.New("forbidden") },
		"no lookup":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			selected := runSelection(t, r, "pull_request", base, head, lookup)
			assert.Equal(t, "true", selected["api"])
			assert.Equal(t, "true", selected["images"])
			assert.JSONEq(t, `{"api":"`+apiKey+`"}`, selected["markers"])
		})
	}
}
