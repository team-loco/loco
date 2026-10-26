package advisorylock

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"strings"
	"testing"
)

const packageDir = "."

func TestKeysAreDistinct(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(packageDir)
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		files = append(files, file)
	}
	pkg, err := new(types.Config).Check("advisorylock", fset, files, nil)
	if err != nil {
		t.Fatalf("type-check package: %v", err)
	}

	owners := map[int64]string{}
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		lockKey, ok := scope.Lookup(name).(*types.Const)
		if !ok {
			continue
		}
		value, exact := constant.Int64Val(lockKey.Val())
		if !exact {
			t.Fatalf("%s is not an int64", name)
		}
		if owner, taken := owners[value]; taken {
			t.Errorf("%s and %s share lock key %#x", owner, name, value)
		}
		owners[value] = name
	}
	if len(owners) == 0 {
		t.Fatal("found no lock keys")
	}
}
