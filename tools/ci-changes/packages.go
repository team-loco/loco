package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// goRepo holds what `go list -deps -test` reports in each Go module of a
// repository.
type goRepo struct {
	dir string
	// packages maps each module directory to the packages listed in it by
	// import path, test variants included.
	packages map[string]map[string]goPackage
}

type goPackage struct {
	ImportPath    string   `json:"ImportPath"`
	Dir           string   `json:"Dir"`
	ForTest       string   `json:"ForTest"`
	Match         []string `json:"Match"`
	Deps          []string `json:"Deps"`
	EmbedPatterns []string `json:"EmbedPatterns"`
	Module        *struct {
		GoMod string `json:"GoMod"`
	} `json:"Module"`
}

// loadGoRepo lists, in each module of the repository in dir, every package of
// the module and the packages roots name in it, with what they and their tests
// import. Modules are not downloaded: an import from outside the repository
// does not resolve, and imports inside it resolve through the replacements.
func loadGoRepo(ctx context.Context, dir string, roots []string) (*goRepo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	modFiles, err := lsFiles(ctx, dir, "*go.mod")
	if err != nil {
		return nil, err
	}
	repo := &goRepo{dir: abs, packages: map[string]map[string]goPackage{}}
	patterns := map[string][]string{}
	for _, file := range modFiles {
		repo.packages[path.Dir(file)] = nil
		patterns[path.Dir(file)] = []string{"./..."}
	}
	for _, root := range roots {
		module := repo.owner(root + "/")
		patterns[module] = append(patterns[module], listPattern(module, root))
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for module, args := range patterns {
		wg.Go(func() {
			listed, err := goList(ctx, filepath.Join(abs, module), args)
			mu.Lock()
			defer mu.Unlock()
			repo.packages[module] = listed
			if err != nil {
				errs = append(errs, fmt.Errorf("module %s: %w", module, err))
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	for _, root := range roots {
		if len(repo.matching(root)) == 0 {
			return nil, fmt.Errorf("%s is not a Go package", root)
		}
	}
	return repo, nil
}

func goList(ctx context.Context, dir string, patterns []string) (map[string]goPackage, error) {
	args := append([]string{"list", "-e", "-deps", "-test", "-tags=conformance",
		"-json=ImportPath,Dir,ForTest,Match,Deps,EmbedPatterns,Module"}, patterns...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	// go list reports directories under PWD.
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOWORK=off", "PWD="+dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w: %s", err, stderr.String())
	}
	packages := map[string]goPackage{}
	for decoder := json.NewDecoder(bytes.NewReader(out)); ; {
		var pkg goPackage
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			return packages, nil
		} else if err != nil {
			return nil, err
		}
		packages[pkg.ImportPath] = pkg
	}
}

// listPattern returns the go list pattern, relative to module, of a package
// directory or of a directory pattern ending in /....
func listPattern(module, root string) string {
	if root == module {
		return "."
	}
	return "./" + strings.TrimPrefix(root, module+"/")
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
		if _, ok := r.packages[dir]; ok {
			return dir
		}
	}
	return dir
}

// matching returns the packages, without test variants, that a root names.
func (r *goRepo) matching(root string) []goPackage {
	module := r.owner(root + "/")
	pattern := listPattern(module, root)
	var matched []goPackage
	for _, pkg := range r.packages[module] {
		if pkg.Dir != "" && pkg.ForTest == "" && slices.Contains(pkg.Match, pattern) {
			matched = append(matched, pkg)
		}
	}
	return matched
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

// inputs returns what building the packages in builds and testing the packages
// in tests reads: their files, the repository packages they import
// transitively, and the go.mod files of the modules those come from. A root
// ending in /... names every package of its module at or under its directory.
// Every root must be one loadGoRepo was given.
func (r *goRepo) inputs(builds, tests []string) goInputs {
	in := goInputs{sources: map[string]bool{}, trees: map[string]bool{}, files: map[string]bool{}}
	for _, entry := range []struct {
		roots []string
		test  bool
	}{{builds, false}, {tests, true}} {
		for _, root := range entry.roots {
			module := r.owner(root + "/")
			listed := r.packages[module]
			in.files[path.Join(module, "go.sum")] = true
			for _, pkg := range r.matching(root) {
				deps := pkg.Deps
				if entry.test {
					in.trees[r.rel(pkg.Dir)] = true
					if binary, ok := listed[pkg.ImportPath+".test"]; ok {
						deps = binary.Deps
					}
				}
				for _, dep := range slices.Concat(deps, []string{pkg.ImportPath}) {
					dep := listed[dep]
					dir := r.rel(dep.Dir)
					if dir == "" {
						continue
					}
					in.sources[dir] = true
					if dep.Module != nil {
						in.files[r.rel(dep.Module.GoMod)] = true
					}
					for _, pattern := range dep.EmbedPatterns {
						in.embeds = append(in.embeds, path.Join(dir, strings.TrimPrefix(pattern, "all:")))
					}
				}
			}
		}
	}
	return in
}

// rel returns the repository path of an absolute path inside the repository,
// or "" for one outside it.
func (r *goRepo) rel(name string) string {
	rel, err := filepath.Rel(r.dir, name)
	if err != nil || name == "" || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return filepath.ToSlash(rel)
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
