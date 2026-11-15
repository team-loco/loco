package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
)

// rootModule is the directory of the repository's root Go module.
const rootModule = "."

var (
	// everyModuleInputs set up, lint or select the Go jobs of every module.
	everyModuleInputs = mustFilter(
		"mise.toml", "mise.lock", ".golangci.yml", "mise-tasks/lint/go",
		".github/actions/setup-tools/**", "tools/ci-changes/**", ".github/workflows/go-lint-and-test.yml",
	)
	rootModuleFiles = mustFilter("go.mod", "go.sum", "internal/**", "gen/go/**", "tools/**", "schemas/**")
	// testInputs are files outside a module that its test task reads.
	testInputs = map[string]filter{
		"observability-proxy": mustFilter(
			"mise-tasks/test/counter-metrics", "mise-tasks/test/lib/ports.sh",
			"charts/loco-worker/values.yaml", "charts/loco-control-plane/values.yaml",
			// The proxy's task also runs these root packages' tests against ClickHouse.
			"internal/clickhouse/**",
		),
		"api": mustFilter(
			"mise-tasks/test/api", "mise-tasks/test/lib/clickhouse.sh", "charts/loco-control-plane/values.yaml",
		),
	}
)

// moduleUsers maps each Go module directory in a repository to the module
// directories whose go.mod replaces a dependency with it.
type moduleUsers map[string][]string

func loadModules(ctx context.Context, dir string) (moduleUsers, error) {
	out, err := git(ctx, dir, "ls-files", "-z", "*go.mod")
	if err != nil {
		return nil, err
	}
	files := strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 })
	users := moduleUsers{}
	for _, file := range files {
		users[path.Dir(file)] = nil
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return nil, err
		}
		parsed, err := modfile.Parse(file, data, nil)
		if err != nil {
			return nil, err
		}
		module := path.Dir(file)
		for _, replace := range parsed.Replace {
			dependency := path.Join(module, replace.New.Path)
			if _, ok := users[dependency]; ok && modfile.IsDirectoryPath(replace.New.Path) {
				users[dependency] = append(users[dependency], module)
			}
		}
	}
	return users, nil
}

// affected returns the sorted directories of the modules whose lint and tests
// a change to files needs. A source change affects its module and every module
// that replaces a dependency with it, a test change only its module. Nil files
// affect every module.
func (m moduleUsers) affected(files []string) []string {
	if files == nil || slices.ContainsFunc(files, everyModuleInputs.matches) {
		return slices.Sorted(maps.Keys(m))
	}
	selected := map[string]bool{}
	for _, file := range files {
		for module, inputs := range testInputs {
			if inputs.matches(file) {
				selected[module] = true
			}
		}
		owner, ok := m.owner(file)
		if !ok {
			continue
		}
		selected[owner] = true
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		for pending := []string{owner}; len(pending) > 0; {
			module := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			for _, user := range m[module] {
				if !selected[user] {
					selected[user] = true
					pending = append(pending, user)
				}
			}
		}
	}
	modules := slices.AppendSeq([]string{}, maps.Keys(selected))
	slices.Sort(modules)
	return modules
}

// owner returns the innermost module directory holding file.
func (m moduleUsers) owner(file string) (string, bool) {
	for dir := path.Dir(file); dir != rootModule; dir = path.Dir(dir) {
		if _, ok := m[dir]; ok {
			return dir, true
		}
	}
	return rootModule, rootModuleFiles.matches(file)
}

func mustFilter(patterns ...string) filter {
	f, err := compileFilter(patterns)
	if err != nil {
		panic(fmt.Sprintf("compile %q: %v", patterns, err))
	}
	return f
}
