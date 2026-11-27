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
		{"go.{mod,sum}", []string{goModFile, "go.sum"}, []string{"go.work"}},
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

func TestCompileRejectsUnsupportedPatterns(t *testing.T) {
	for _, pattern := range []string{"api/[ab].go", "a+", "?.go", "api/{a,b", "a}", "!**/*_test.go"} {
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
		withUsers(filters, selectOwn(filters, "pull_request", []string{"mise.toml"})))
	assert.Equal(t, map[string]bool{release: false, review: false},
		withUsers(filters, selectOwn(filters, "push", []string{"mise.toml"})), "the push list omits mise.toml")
	assert.Equal(t, map[string]bool{release: true, review: false},
		withUsers(filters, selectOwn(filters, "push", nil)), "a workflow without a push list never runs on push")
	assert.Equal(t, map[string]bool{release: false, review: false},
		withUsers(filters, selectOwn(filters, "pull_request", []string{})))
	assert.Equal(t, map[string]bool{release: true, review: true},
		withUsers(filters, selectOwn(filters, dispatchEvent, nil)), "a dispatch runs every workflow")
}

func TestWorkflowRunsWheneverAWorkflowItIsForRuns(t *testing.T) {
	const images, e2e, charts, docs = "images", "e2e", "charts", "docs"
	config := filepath.Join(t.TempDir(), "paths.yml")
	require.NoError(t, os.WriteFile(config, []byte(`
images:
  for: [e2e, charts]
  push: ["**"]
e2e:
  pull_request: [api/**]
charts:
  pull_request: [charts/**]
docs:
  pull_request: [docs/**]
`), 0o600))
	filters, err := loadFilters(config)
	require.NoError(t, err)

	assert.Equal(t, map[string]bool{images: true, e2e: false, charts: true, docs: false},
		withUsers(filters, selectOwn(filters, "pull_request", []string{"charts/a.yaml"})))
	assert.Equal(t, map[string]bool{images: false, e2e: false, charts: false, docs: true},
		withUsers(filters, selectOwn(filters, "pull_request", []string{"docs/a.md"})))
	assert.Equal(t, map[string]bool{images: true, e2e: false, charts: false, docs: false},
		withUsers(filters, selectOwn(filters, "push", []string{"docs/a.md"})), "its own push list still applies")
}

func TestLoadRejectsAnUnknownUser(t *testing.T) {
	config := filepath.Join(t.TempDir(), "paths.yml")
	require.NoError(t, os.WriteFile(config, []byte("images:\n  for: [e2e]\n"), 0o600))
	_, err := loadFilters(config)
	assert.ErrorContains(t, err, `for names "e2e"`)
}

func TestLoadRejectsUnknownEvents(t *testing.T) {
	config := filepath.Join(t.TempDir(), "paths.yml")
	require.NoError(t, os.WriteFile(config, []byte("docs:\n  pull-request: [docs/**]\n"), 0o600))
	_, err := loadFilters(config)
	assert.ErrorContains(t, err, "field pull-request not found")
}

func TestChangedFiles(t *testing.T) {
	r := newRepo(t)
	root := r.write("README.md", "README.md")
	r.git("checkout", "-q", "-b", "feature")
	r.write("api/a.go", "api/a.go")
	head := r.write("docs/b.md", "docs/b.md")
	r.git("checkout", "-q", "main")
	base := r.write("charts/c.yaml", "charts/c.yaml")
	files, err := changedFiles(t.Context(), r.dir, "pull_request", base, head)
	require.NoError(t, err)
	assert.Equal(t, []string{"api/a.go", "docs/b.md"}, files,
		"a pull request diffs from the merge base, leaving out commits only the base has")

	files, err = changedFiles(t.Context(), r.dir, "push", root, base)
	require.NoError(t, err)
	assert.Equal(t, []string{"charts/c.yaml"}, files)

	for _, before := range []string{"", zeroSHA} {
		files, err = changedFiles(t.Context(), r.dir, "push", before, base)
		require.NoError(t, err)
		assert.Nil(t, files, "a push without a previous commit changes everything")
	}

	files, err = changedFiles(t.Context(), r.dir, "pull_request", base, base)
	require.NoError(t, err)
	assert.Equal(t, []string{}, files, "an empty diff changes nothing")

	_, err = changedFiles(t.Context(), r.dir, "schedule", base, head)
	assert.Error(t, err)
}

func TestAffectedE2EShards(t *testing.T) {
	const (
		apps     = "apps"
		appsTest = "tests/e2e/apps/a_test.go"
	)
	workflows, _ := repository(t)
	all := []string{apps, "builds", "routing", "telemetry"}
	for _, tc := range []struct {
		files, want []string
	}{
		{nil, all},
		{[]string{"tests/e2e/routing/a_test.go"}, []string{"routing"}},
		{[]string{appsTest, "docs/content/example.md"}, []string{apps}},
		{[]string{"tests/e2e/telemetry/a_test.go", appsTest}, []string{apps, "telemetry"}},
		{[]string{appsTest, "api/service/resource.go"}, all},
		{[]string{"tests/e2e/platform/setup.go"}, all},
		{[]string{"tests/e2e/platform/setup_test.go"}, nil},
		{[]string{"tests/e2e/fixtures/example"}, all},
	} {
		assert.Equal(t, tc.want, affectedShards(workflows["e2e"].matcher("pull_request"), tc.files), tc.files)
	}
}

func TestInstallationRunnerUnitTests(t *testing.T) {
	workflows, _ := repository(t)
	for _, dir := range []string{"tools/installation-e2e/", "tools/internal/devicelogin/", "tools/platform-release/"} {
		files := []string{dir + "client_test.go", dir + "client.go"}
		selected := selectOwn(workflows, "pull_request", files[:1])
		assert.False(t, selected["e2e-installation"])
		assert.True(t, selected["go-lint-and-test"])
		assert.True(t, selectOwn(workflows, "pull_request", files)["e2e-installation"])
	}
}

func TestAGoChangeRunsTheSuitesImportingIt(t *testing.T) {
	workflows, _ := repository(t)
	suites := strings.Fields("auth-conformance charts controller-helm-chart docker e2e e2e-installation " +
		"go-lint-and-test obs-chart platform-release transit-conformance web")
	const apiSuites = "auth-conformance charts docker e2e e2e-installation go-lint-and-test web"
	for file, runs := range map[string]string{
		"internal/envconfig/envconfig.go":      "charts docker e2e e2e-installation go-lint-and-test obs-chart web",
		"internal/envconfig/envconfig_test.go": "go-lint-and-test",
		"api/service/config.go":                apiSuites,
		"api/service/account_deletion.go":      apiSuites,
		"api/pkg/transit/token.go": "auth-conformance charts docker e2e e2e-installation go-lint-and-test " +
			"transit-conformance web",
		"api/migrations/00004_events.sql": apiSuites,
		"tests/e2e/apps/main_test.go":     "e2e go-lint-and-test",
		"tools/pullimage/main.go":         "charts e2e go-lint-and-test obs-chart web",
		"k8sapi/v1alpha1/application_types.go": "auth-conformance charts controller-helm-chart docker e2e " +
			"e2e-installation go-lint-and-test web",
	} {
		selected := selectOwn(workflows, "pull_request", []string{file})
		var got []string
		for _, suite := range suites {
			if selected[suite] {
				got = append(got, suite)
			}
		}
		assert.Equal(t, strings.Fields(runs), got, file)
	}
}
