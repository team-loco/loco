package main

import (
	"fmt"
	"maps"
	"slices"
)

// rootModule is the directory of the repository's root Go module.
const rootModule = "."

// moduleWorkflow lints and tests the Go modules the go-modules output lists.
const moduleWorkflow = "go-lint-and-test"

var (
	// everyModuleInputs set up, lint or select the Go jobs of every module.
	everyModuleInputs = mustFilter(
		"mise.toml", "mise.lock", ".golangci.yml", "mise-tasks/lint/go",
		".github/actions/setup-tools/**", "tools/ci-changes/**", ".github/workflows/go-lint-and-test.yml",
	)
	// testInputs are files outside a module that its test task reads.
	testInputs = map[string]filter{
		"observability-proxy": mustFilter(
			"mise-tasks/test/counter-metrics", "mise-tasks/test/lib/testres.sh",
			"charts/loco-worker/values.yaml", "charts/loco-control-plane/values.yaml",
			// The proxy's task also runs these root packages' tests against ClickHouse.
			"internal/clickhouse/**",
		),
		"api": mustFilter(
			"mise-tasks/test/api", "mise-tasks/test/lib/clickhouse.sh", "mise-tasks/test/lib/testres.sh",
			"charts/loco-control-plane/values.yaml",
			"docs/content/reference/api-environment.md",
		),
	}
)

// moduleInputs maps each Go module directory to the files its lint and tests
// read: every package of the module with its tests, what those import, and
// for a module below the root every file in its directory.
type moduleInputs map[string]func(file string) bool

func loadModuleInputs(repo *goRepo) (moduleInputs, error) {
	modules := moduleInputs{}
	for module := range repo.modules {
		in, err := repo.inputs(nil, repo.modulePackages(module))
		if err != nil {
			return nil, fmt.Errorf("module %s: %w", module, err)
		}
		modules[module] = func(file string) bool {
			return in.matches(file) || module != rootModule && repo.owner(file) == module ||
				testInputs[module] != nil && testInputs[module].matches(file)
		}
	}
	return modules, nil
}

// affected returns the sorted directories of the modules whose lint and tests
// a change to files needs. Nil files affect every module.
func (m moduleInputs) affected(files []string) []string {
	if files == nil || slices.ContainsFunc(files, everyModuleInputs.matches) {
		return slices.Sorted(maps.Keys(m))
	}
	modules := []string{}
	for module, inputs := range m {
		if slices.ContainsFunc(files, inputs) {
			modules = append(modules, module)
		}
	}
	slices.Sort(modules)
	return modules
}

// matches reports whether a change to file affects any module.
func (m moduleInputs) matches(file string) bool {
	return everyModuleInputs.matches(file) || slices.ContainsFunc(slices.Collect(maps.Values(m)),
		func(inputs func(string) bool) bool { return inputs(file) })
}

func mustFilter(patterns ...string) filter {
	f, err := compileFilter(patterns)
	if err != nil {
		panic(fmt.Sprintf("compile %q: %v", patterns, err))
	}
	return f
}
