package main

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	apiModule        = "api"
	controllerModule = "controller"
	proxyModule      = "observability-proxy"
)

func repositoryModules(t *testing.T) moduleUsers {
	t.Helper()
	modules, err := loadModules(t.Context(), "../..")
	require.NoError(t, err)
	return modules
}

func TestModulesAChangeAffects(t *testing.T) {
	modules := repositoryModules(t)
	rootUsers := []string{".", "agent", apiModule, "builder", "cmd", controllerModule, proxyModule, "tests/e2e"}
	for _, tc := range []struct {
		name     string
		files    []string
		expected []string
	}{
		{"a proxy test and docs select only the proxy",
			[]string{"observability-proxy/pkg/ingest/gate_test.go", "docs/content/operations/self-hosting.md"},
			[]string{proxyModule}},
		{"an API change selects only the API", []string{"api/service/token.go"}, []string{apiModule}},
		{"a shared package selects its users", []string{"internal/buildinfo/version.go"}, rootUsers},
		{"generated code selects the root users", []string{"gen/go/loco/token/v1/token.pb.go"}, rootUsers},
		{"a schema selects the root users", []string{"schemas/loco.v1.json"}, rootUsers},
		{"the root go.mod selects its users", []string{"go.mod"}, rootUsers},
		{"CLI changes select only the CLI",
			[]string{"cmd/loco/main.go", "cmd/loco/main_test.go", "cmd/internal/ui/select.go", "cmd/go.mod"},
			[]string{"cmd"}},
		{"Kubernetes types select every importing module", []string{"k8sapi/v1/application_types.go"},
			[]string{"agent", apiModule, controllerModule, "k8sapi", "tests/e2e"}},
		{"a Kubernetes types test selects only its module", []string{"k8sapi/v1/application_test.go"}, []string{"k8sapi"}},
		{"the proxy's counter metrics task", []string{"mise-tasks/test/counter-metrics"}, []string{proxyModule}},
		{"the proxy's port helper", []string{"mise-tasks/test/lib/ports.sh"}, []string{proxyModule}},
		{"the worker chart values", []string{"charts/loco-worker/values.yaml"}, []string{proxyModule}},
		{"a file at a module's root", []string{"controller/Makefile"}, []string{controllerModule}},
		{"a ClickHouse query test runs in the root and proxy jobs",
			[]string{"internal/clickhouse/logs_test.go"}, []string{".", proxyModule}},
		{"the API test task", []string{"mise-tasks/test/api"}, []string{apiModule}},
		{"the ClickHouse helper of the API tests", []string{"mise-tasks/test/lib/clickhouse.sh"}, []string{apiModule}},
		{"the control plane chart values",
			[]string{"charts/loco-control-plane/values.yaml"}, []string{apiModule, proxyModule}},
		{"a fixture app selects only its own module",
			[]string{"tests/e2e/fixtures/build-app/main.go"}, []string{"tests/e2e/fixtures/build-app"}},
		{"documentation selects nothing", []string{"docs/content/operations/self-hosting.md"}, []string{}},
		{"no changes select nothing", []string{}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, modules.affected(tc.files))
		})
	}
}

func TestToolchainAndSelectionChangesAffectEveryModule(t *testing.T) {
	modules := repositoryModules(t)
	every := slices.Sorted(maps.Keys(modules))
	require.Contains(t, every, "k8sapi")
	for _, file := range []string{
		"mise-tasks/lint/go",
		".golangci.yml",
		".github/actions/setup-tools/action.yml",
		"tools/ci-changes/modules.go",
		".github/workflows/go-lint-and-test.yml",
	} {
		assert.Equal(t, every, modules.affected([]string{file}), file)
	}
	assert.Equal(t, every, modules.affected(nil), "a push without a previous commit affects every module")
}

func TestSelectionWritesTheAffectedModules(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module example.test/root\n")
	r.write("observability-proxy/go.mod",
		"module example.test/proxy\n\nreplace example.test/root => ../\n")
	r.write("tools/go.mod", "module example.test/tools\n\nreplace example.test/root => ../missing\n")
	base := r.git("rev-parse", "HEAD")
	r.write("observability-proxy/validator_test.go", "test")
	head := r.write("docs/self-hosting.md", "documentation")

	assert.Equal(t, `["observability-proxy"]`, runSelection(t, r, "pull_request", base, head, nil)["go-modules"])

	head = r.write("internal/a.go", "package internal")
	assert.Equal(t, `[".","observability-proxy"]`, runSelection(t, r, "push", base, head, nil)["go-modules"],
		"a replace pointing outside the repository's modules links nothing")
}
