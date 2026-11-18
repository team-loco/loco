package main

import (
	"context"
	"errors"
	"fmt"
	"go/build"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
)

// goRepo holds the Go modules and packages of a repository.
type goRepo struct {
	// modules maps each module directory to its module.
	modules map[string]goModule
	// packages maps each directory holding Go files to its package.
	packages map[string]*build.Package
}

type goModule struct {
	path string
	// replaces lists the directories of the repository modules its go.mod
	// replaces a dependency with.
	replaces []string
}

func loadGoRepo(ctx context.Context, dir string) (*goRepo, error) {
	modFiles, err := lsFiles(ctx, dir, "*go.mod")
	if err != nil {
		return nil, err
	}
	repo := &goRepo{modules: map[string]goModule{}, packages: map[string]*build.Package{}}
	parsed := map[string]*modfile.File{}
	for _, file := range modFiles {
		data, readErr := os.ReadFile(filepath.Join(dir, file))
		if readErr != nil {
			return nil, readErr
		}
		if parsed[path.Dir(file)], err = modfile.Parse(file, data, nil); err != nil {
			return nil, err
		}
	}
	for module, f := range parsed {
		m := goModule{path: f.Module.Mod.Path}
		for _, replace := range f.Replace {
			target := path.Join(module, replace.New.Path)
			if _, ok := parsed[target]; ok && modfile.IsDirectoryPath(replace.New.Path) {
				m.replaces = append(m.replaces, target)
			}
		}
		repo.modules[module] = m
	}
	goFiles, err := lsFiles(ctx, dir, "*.go")
	if err != nil {
		return nil, err
	}
	// Every file counts, whatever its build constraints, so a suite's inputs
	// cover each platform and build tag it runs with.
	buildContext := build.Default
	buildContext.UseAllFiles = true
	for _, file := range goFiles {
		pkgDir := path.Dir(file)
		if repo.packages[pkgDir] != nil {
			continue
		}
		// A package that does not parse keeps the imports read before the
		// error; its module's lint reports the error.
		pkg, err := buildContext.ImportDir(filepath.Join(dir, pkgDir), 0)
		if err != nil && !errors.As(err, new(*build.NoGoError)) {
			slog.Warn("read Go package", "dir", pkgDir, "error", err)
		}
		repo.packages[pkgDir] = pkg
	}
	return repo, nil
}

func lsFiles(ctx context.Context, dir, pattern string) ([]string, error) {
	out, err := git(ctx, dir, "ls-files", "-z", pattern)
	if err != nil {
		return nil, err
	}
	return strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 }), nil
}

// owner returns the innermost module directory holding file.
func (r *goRepo) owner(file string) string {
	dir := path.Dir(file)
	for ; dir != rootModule; dir = path.Dir(dir) {
		if _, ok := r.modules[dir]; ok {
			return dir
		}
	}
	return dir
}

// modulePackages returns the package directories `go list ./...` lists in a module.
func (r *goRepo) modulePackages(module string) []string {
	var dirs []string
	for dir := range r.packages {
		if r.owner(dir+"/") == module && !slices.ContainsFunc(strings.Split(dir, "/"), func(e string) bool {
			return e == "testdata" || strings.HasPrefix(e, "_") || strings.HasPrefix(e, ".") && e != "."
		}) {
			dirs = append(dirs, dir)
		}
	}
	slices.Sort(dirs)
	return dirs
}

// resolve returns the directory of the repository package an import of the
// build of main names. The main module's replacements decide which repository
// modules the build reads.
func (r *goRepo) resolve(main, importPath string) (string, bool) {
	module, best := "", -1
	for dir, m := range r.modules {
		if (importPath == m.path || strings.HasPrefix(importPath, m.path+"/")) && len(m.path) > best {
			module, best = dir, len(m.path)
		}
	}
	if best < 0 || module != main && !slices.Contains(r.modules[main].replaces, module) {
		return "", false
	}
	dir := path.Join(module, strings.TrimPrefix(importPath[best:], "/"))
	_, ok := r.packages[dir]
	return dir, ok
}

// goInputs are the repository files a set of Go builds and tests reads.
type goInputs struct {
	// sources holds the directories whose non-test Go files count.
	sources map[string]bool
	// trees holds the directories of tested packages, every file under which counts.
	trees map[string]bool
	files map[string]bool
	// embeds holds the go:embed patterns of the sources, joined to their package directory.
	embeds []string
}

// inputs returns what building the packages in build and testing the packages
// in test reads: their files, the packages they import transitively, and the
// go.mod files of the modules those come from. A pattern ending in /... names
// every package of its module at or under its directory.
func (r *goRepo) inputs(builds, tests []string) (goInputs, error) {
	in := goInputs{sources: map[string]bool{}, trees: map[string]bool{}, files: map[string]bool{}}
	type node struct{ dir, main string }
	var pending []node
	for _, entry := range []struct {
		patterns []string
		test     bool
	}{{builds, false}, {tests, true}} {
		for _, pattern := range entry.patterns {
			dirs, err := r.expand(pattern)
			if err != nil {
				return goInputs{}, err
			}
			for _, dir := range dirs {
				main := r.owner(dir + "/")
				in.files[path.Join(main, "go.sum")] = true
				pending = append(pending, node{dir, main})
				if !entry.test {
					continue
				}
				in.trees[dir] = true
				pkg := r.packages[dir]
				for _, imp := range slices.Concat(pkg.TestImports, pkg.XTestImports) {
					if dep, ok := r.resolve(main, imp); ok {
						pending = append(pending, node{dep, main})
					}
				}
			}
		}
	}
	seen := map[node]bool{}
	for len(pending) > 0 {
		n := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		in.sources[n.dir] = true
		in.files[path.Join(r.owner(n.dir+"/"), "go.mod")] = true
		pkg := r.packages[n.dir]
		for _, pattern := range pkg.EmbedPatterns {
			in.embeds = append(in.embeds, path.Join(n.dir, strings.TrimPrefix(pattern, "all:")))
		}
		for _, imp := range pkg.Imports {
			if dep, ok := r.resolve(n.main, imp); ok {
				pending = append(pending, node{dep, n.main})
			}
		}
	}
	return in, nil
}

func (r *goRepo) expand(pattern string) ([]string, error) {
	if base, ok := strings.CutSuffix(pattern, "/..."); ok {
		var dirs []string
		for _, dir := range r.modulePackages(r.owner(base + "/")) {
			if dir == base || strings.HasPrefix(dir, base+"/") {
				dirs = append(dirs, dir)
			}
		}
		if len(dirs) == 0 {
			return nil, fmt.Errorf("%s matches no Go package", pattern)
		}
		return dirs, nil
	}
	if _, ok := r.packages[pattern]; !ok {
		return nil, fmt.Errorf("%s is not a Go package", pattern)
	}
	return []string{pattern}, nil
}

func (in goInputs) matches(file string) bool {
	if in.files[file] {
		return true
	}
	dir := path.Dir(file)
	if in.sources[dir] && strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") {
		return true
	}
	for d := dir; ; d = path.Dir(d) {
		if in.trees[d] {
			return true
		}
		if d == rootModule {
			break
		}
	}
	// An embed pattern names a file or a directory whose files it embeds.
	return slices.ContainsFunc(in.embeds, func(pattern string) bool {
		for f := file; f != rootModule; f = path.Dir(f) {
			if matched, err := path.Match(pattern, f); err == nil && matched {
				return true
			}
		}
		return false
	})
}
