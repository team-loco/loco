package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/team-loco/loco/api/limits"
)

const apiEnvironmentPage = "../docs/content/reference/api-environment.md"

var envNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)

// readEnvNames returns the environment variable names in this package's non-test sources and the
// count limits.
func readEnvNames(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, unquoteErr := strconv.Unquote(lit.Value)
			if unquoteErr == nil && envNamePattern.MatchString(value) {
				names = append(names, value)
			}
			return true
		})
	}
	for _, limit := range limits.All {
		names = append(names, limit.Env)
	}
	return names
}

func TestEnvironmentVariablesDocumented(t *testing.T) {
	page, err := os.ReadFile(apiEnvironmentPage)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range readEnvNames(t) {
		if !strings.Contains(string(page), "| `"+name+"` |") {
			t.Errorf("%s has no row in %s", name, apiEnvironmentPage)
		}
	}
}
