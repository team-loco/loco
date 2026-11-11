package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterMatches(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		matches []string
		misses  []string
	}{
		{"**.proto", []string{"proto/loco/a/v1/a.proto", "a.proto"}, []string{"a.protox"}},
		{"**/Dockerfile", []string{"Dockerfile", "web/Dockerfile"}, []string{"web/Dockerfile.dev"}},
		{"docs/**", []string{"docs/content/a/b.md"}, []string{"docsx/a.md"}},
		{"api/service/event*", []string{"api/service/events.go"}, []string{"api/service/events/a.go"}},
		{"**/*compose*.yaml", []string{"dev/compose.yaml", "compose.yaml"}, []string{"dev/compose.yml"}},
		{"go.{mod,sum}", []string{"go.mod", "go.sum"}, []string{"go.work"}},
		{"api/{main,config}*.go", []string{"api/config_test.go"}, []string{"api/server.go"}},
		{"docs?.md", []string{"doc.md", "docs.md"}, []string{"docss.md"}},
		{".github/workflows/*.yml", []string{".github/workflows/ci.yml"}, []string{".github/workflows/a/b.yml"}},
		{"mise.lock", []string{"mise.lock"}, []string{"mise.lock.bak", "x/mise.lock"}},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			f, err := compileFilter([]string{tc.pattern})
			require.NoError(t, err)
			for _, path := range tc.matches {
				assert.True(t, f.matches(path), path)
			}
			for _, path := range tc.misses {
				assert.False(t, f.matches(path), path)
			}
		})
	}
}

func TestNegationAppliesInOrder(t *testing.T) {
	f, err := compileFilter([]string{"observability-proxy/**", "!**/*_test.go", "observability-proxy/keep_test.go"})
	require.NoError(t, err)

	assert.True(t, f.matches("observability-proxy/main.go"))
	assert.False(t, f.matches("observability-proxy/main_test.go"), "a later negation excludes an earlier match")
	assert.True(t, f.matches("observability-proxy/keep_test.go"), "a later pattern re-includes a negated path")
	assert.False(t, f.matches("api/main.go"))

	negatedFirst, err := compileFilter([]string{"!**/*_test.go", "observability-proxy/**"})
	require.NoError(t, err)
	assert.True(t, negatedFirst.matches("observability-proxy/main_test.go"),
		"a negation before the match excludes nothing")
}

func TestCompileRejectsUnsupportedPatterns(t *testing.T) {
	for _, pattern := range []string{"api/[ab].go", "a+", "?.go", "api/{a,b", "a}"} {
		_, err := compileFilter([]string{pattern})
		require.Error(t, err, pattern)
	}
	_, err := compileFilter(nil)
	require.Error(t, err)
}

func TestSelectUsesTheEventsList(t *testing.T) {
	const release, review = "release", "review"
	config := filepath.Join(t.TempDir(), "paths.yml")
	require.NoError(t, os.WriteFile(config, []byte(`
release:
  pull_request: [mise.toml, charts/**]
  push: [charts/**]
review:
  pull_request: [docs/**]
`), 0o600))
	filters, err := loadFilters(config)
	require.NoError(t, err)

	assert.Equal(t, map[string]bool{release: true, review: false},
		selectWorkflows(filters, "pull_request", []string{"mise.toml"}))
	assert.Equal(t, map[string]bool{release: false, review: false},
		selectWorkflows(filters, "push", []string{"mise.toml"}), "the push list omits mise.toml")
	assert.Equal(t, map[string]bool{release: true, review: false},
		selectWorkflows(filters, "push", nil), "a workflow without a push list never runs on push")
	assert.Equal(t, map[string]bool{release: false, review: false},
		selectWorkflows(filters, "pull_request", []string{}))
}

func TestLoadRejectsUnknownEvents(t *testing.T) {
	config := filepath.Join(t.TempDir(), "paths.yml")
	require.NoError(t, os.WriteFile(config, []byte("docs:\n  pull-request: [docs/**]\n"), 0o600))
	_, err := loadFilters(config)
	assert.ErrorContains(t, err, `unknown event "pull-request"`)
}

func TestInstallationInputsSelectOnlyInstallationE2E(t *testing.T) {
	filters, err := loadFilters("../../.github/ci-paths.yml")
	require.NoError(t, err)
	for _, path := range []string{
		"tools/installation-e2e/workflow.go",
		"tools/installation-e2e/main_test.go",
		".github/workflows/e2e-installation.yml",
		"charts/cert-manager.yaml",
		"dev/compose.cloud-provider-kind.yaml",
		"dev/dex.yaml",
		"examples/installation/installation.yaml",
		"web/src/App.tsx",
		"web/Dockerfile",
		"gen/ts/package.json",
		"bun.lock",
		"docs/content/reference/installation.md",
		"docs/zensical.toml",
		"schemas/loco.v1.json",
	} {
		t.Run(path, func(t *testing.T) {
			selected := selectWorkflows(filters, "pull_request", []string{path})
			assert.True(t, selected["e2e-installation"])
			assert.False(t, selected["e2e"])
		})
	}
}

func TestSuiteInputsSelectBothE2EWorkflows(t *testing.T) {
	filters, err := loadFilters("../../.github/ci-paths.yml")
	require.NoError(t, err)
	for _, path := range []string{
		"api/service/deployment.go",
		"api/service/resource.go",
		"api/queries/resource.sql",
		"cmd/locoadm/main.go",
		"charts/loco-worker/values.yaml",
		"tests/e2e/fixtures/build-app/deploy/Dockerfile",
		"tests/e2e/fixtures/installation/kind.yaml",
		"tests/e2e/fixtures/public-image/Dockerfile",
	} {
		t.Run(path, func(t *testing.T) {
			selected := selectWorkflows(filters, "pull_request", []string{path})
			assert.True(t, selected["e2e-installation"])
			assert.True(t, selected["e2e"])
		})
	}
}

func TestDashboardSuitesSelectOnlyWeb(t *testing.T) {
	filters, err := loadFilters("../../.github/ci-paths.yml")
	require.NoError(t, err)
	for _, path := range []string{
		"tests/ui/new_service_ui.py",
		"tests/ui/not_found_ui.py",
		"tests/ui/resource_details_ui.py",
		"tests/ui/shared_controls_ui.py",
		"tests/ui/webhooks_ui.py",
		"tests/ui/webhooks_api.py",
		"tests/ui/activity_fixture.py",
		"tests/ui/fixtures/deployment-history/scenario.json",
		"mise-tasks/test/ui",
	} {
		t.Run(path, func(t *testing.T) {
			selected := selectWorkflows(filters, "pull_request", []string{path})
			assert.True(t, selected["web"])
			assert.False(t, selected["e2e"])
			assert.False(t, selected["e2e-installation"])
		})
	}
}

func TestKindSuiteInputsSkipInstallationE2E(t *testing.T) {
	filters, err := loadFilters("../../.github/ci-paths.yml")
	require.NoError(t, err)
	for _, path := range []string{
		"tests/e2e/run.sh",
		"tests/e2e/suites/09_resource_status.sh",
		"tests/e2e/fixtures/otel-app/main.go",
		"tools/e2e-auth/main.go",
	} {
		t.Run(path, func(t *testing.T) {
			selected := selectWorkflows(filters, "pull_request", []string{path})
			assert.True(t, selected["e2e"])
			assert.False(t, selected["e2e-installation"])
			assert.False(t, selected["web"])
		})
	}
}

func TestUnitTestChangesSkipE2E(t *testing.T) {
	filters, err := loadFilters("../../.github/ci-paths.yml")
	require.NoError(t, err)
	for _, path := range []string{
		"api/config_test.go",
		"api/service/resource_create_db_test.go",
		"cmd/locoadm/root_test.go",
		"controller/internal/controller/application_controller_test.go",
	} {
		t.Run(path, func(t *testing.T) {
			selected := selectWorkflows(filters, "pull_request", []string{path})
			assert.True(t, selected["test"])
			assert.False(t, selected["e2e"])
			assert.False(t, selected["e2e-installation"])

			selected = selectWorkflows(filters, "pull_request", []string{path, "api/service/resource.go"})
			assert.True(t, selected["e2e"])
			assert.True(t, selected["e2e-installation"])
		})
	}
}

func TestInstallationSchemaSelectsPlatformRelease(t *testing.T) {
	filters, err := loadFilters("../../.github/ci-paths.yml")
	require.NoError(t, err)
	selected := selectWorkflows(filters, "pull_request", []string{"schemas/installation.v1.json"})
	assert.True(t, selected["platform-release"])
}

func TestChangedFiles(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		out, err := git(t.Context(), dir, args...)
		require.NoError(t, err)
		return strings.TrimSpace(string(out))
	}
	commit := func(path string) string {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(path), 0o600))
		run("add", path)
		run("-c", "user.name=ci", "-c", "user.email=ci@loco.test", "commit", "-q", "-m", path)
		return run("rev-parse", "HEAD")
	}
	run("init", "-q", "-b", "main")
	root := commit("README.md")
	run("checkout", "-q", "-b", "feature")
	commit("api/a.go")
	head := commit("docs/b.md")
	run("checkout", "-q", "main")
	base := commit("charts/c.yaml")

	files, err := changedFiles(t.Context(), dir, "pull_request", base, head)
	require.NoError(t, err)
	assert.Equal(t, []string{"api/a.go", "docs/b.md"}, files,
		"a pull request diffs from the merge base, leaving out commits only the base has")

	files, err = changedFiles(t.Context(), dir, "push", root, base)
	require.NoError(t, err)
	assert.Equal(t, []string{"charts/c.yaml"}, files)

	for _, before := range []string{"", zeroSHA} {
		files, err = changedFiles(t.Context(), dir, "push", before, base)
		require.NoError(t, err)
		assert.Nil(t, files, "a push without a previous commit changes everything")
	}

	files, err = changedFiles(t.Context(), dir, "pull_request", base, base)
	require.NoError(t, err)
	assert.Equal(t, []string{}, files, "an empty diff changes nothing")

	_, err = changedFiles(t.Context(), dir, "workflow_dispatch", base, head)
	assert.Error(t, err)
}
