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
	agentModule      = "agent"
	cmdModule        = "cmd"
	e2eModule        = "tests/e2e"
	goModFile        = "go.mod"
)

// repository returns the repository's filters, with their Go inputs, and the
// inputs of each Go module.
func repository(t *testing.T) (map[string]workflowFilter, moduleInputs) {
	t.Helper()
	all, err := loadFilters("../../.github/ci-paths.yml")
	require.NoError(t, err)
	modules, err := resolveGo(t.Context(), "../..", all)
	require.NoError(t, err)
	return all, modules
}

func repositoryModules(t *testing.T) moduleInputs {
	t.Helper()
	_, modules := repository(t)
	return modules
}

func TestModulesAChangeAffects(t *testing.T) {
	modules := repositoryModules(t)
	rootUsers := []string{".", agentModule, apiModule, "builder", cmdModule, controllerModule, proxyModule, e2eModule}
	for _, tc := range []struct {
		name     string
		files    []string
		expected []string
	}{
		{"a proxy test and docs select only the proxy",
			[]string{"observability-proxy/pkg/ingest/gate_test.go", "docs/content/operations/self-hosting.md"},
			[]string{proxyModule}},
		{"an API change selects only the API", []string{"api/service/token.go"}, []string{apiModule}},
		{"a shared package selects the modules importing it", []string{"internal/buildinfo/version.go"},
			[]string{".", agentModule, apiModule, "builder", cmdModule, controllerModule, proxyModule}},
		{"a shared package's test selects only its module", []string{"internal/envconfig/envconfig_test.go"},
			[]string{"."}},
		{"generated code selects the modules importing it", []string{"gen/go/loco/token/v1/token.pb.go"},
			[]string{".", apiModule, cmdModule, proxyModule, e2eModule}},
		{"an embedded schema selects the modules importing its package", []string{"schemas/loco.v1.json"},
			[]string{".", cmdModule}},
		{"a file read by a root package's tests selects the root module",
			[]string{"tools/platform-release/testdata/bundle.json"}, []string{"."}},
		{"the root go.mod selects the modules reading it", []string{goModFile}, rootUsers},
		{"CLI changes select only the CLI",
			[]string{"cmd/loco/main.go", "cmd/loco/main_test.go", "cmd/internal/ui/select.go", "cmd/go.mod"},
			[]string{cmdModule}},
		{"Kubernetes types select every importing module", []string{"k8sapi/v1alpha1/application_types.go"},
			[]string{agentModule, apiModule, controllerModule, "k8sapi", e2eModule}},
		{"a Kubernetes types test selects only its module", []string{"k8sapi/v1alpha1/application_test.go"},
			[]string{"k8sapi"}},
		{"the proxy's counter metrics task", []string{"mise-tasks/test/counter-metrics"}, []string{proxyModule}},
		{"the lease helper of the test tasks", []string{"mise-tasks/test/lib/testres.sh"}, []string{apiModule, proxyModule}},
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

func TestGoInputsFollowTheMainModulesReplacements(t *testing.T) {
	r := newRepo(t)
	r.write(goModFile, "module example.test/root\n")
	r.write("shared/shared.go", "package shared\n\nimport _ \"embed\"\n\n//go:embed assets\nvar assets string\n")
	r.write("shared/assets/logo.svg", "svg")
	r.write("shared/shared_test.go", "package shared\n\nimport _ \"example.test/root/testonly\"\n")
	r.write("testonly/testonly.go", "package testonly\n")
	r.write("unused/unused.go", "package unused\n")
	r.write("app/go.mod",
		"module example.test/app\n\nrequire example.test/root v0.0.0\n\nreplace example.test/root => ../\n")
	r.write("app/main.go", "package main\n\nimport _ \"example.test/root/shared\"\n")
	r.write("pinned/go.mod", "module example.test/pinned\n")
	r.write("pinned/main.go", "package main\n\nimport _ \"example.test/root/shared\"\n")
	repo, err := loadGoRepo(t.Context(), r.dir, []string{"app", "pinned", "shared"})
	require.NoError(t, err)

	app := repo.inputs([]string{"app"}, nil)
	for _, file := range []string{
		"app/main.go", "app/go.mod", "app/go.sum", goModFile, "shared/new.go", "shared/assets/logo.svg",
	} {
		assert.True(t, app.matches(file), file)
	}
	for _, file := range []string{"shared/shared_test.go", "testonly/testonly.go", "unused/unused.go", "go.sum"} {
		assert.False(t, app.matches(file), file)
	}

	pinned := repo.inputs([]string{"pinned"}, nil)
	assert.False(t, pinned.matches("shared/shared.go"), "without a replacement the build reads a published version")

	tested := repo.inputs(nil, []string{"shared"})
	for _, file := range []string{"shared/shared_test.go", "shared/testdata/case.json", "testonly/testonly.go"} {
		assert.True(t, tested.matches(file), file)
	}

	_, err = loadGoRepo(t.Context(), r.dir, []string{"missing"})
	assert.ErrorContains(t, err, "missing is not a Go package")
}
