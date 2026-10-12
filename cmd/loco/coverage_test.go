package loco

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

const coverCounterPrefix = "covcounters."

func TestScriptsIsolateCoverage(t *testing.T) {
	shared := os.Getenv(coverDirEnv)
	if shared == "" {
		t.Skip("coverage is off")
	}
	scriptDir := filepath.Join("testdata", "coverage")
	scripts, err := filepath.Glob(filepath.Join(scriptDir, "*.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	countersBefore := countCoverCounters(t, shared)

	var mu sync.Mutex
	var dirs []string
	t.Cleanup(func() {
		if len(dirs) != len(scripts) {
			t.Fatalf("recorded %d coverage directories for %d scripts", len(dirs), len(scripts))
		}
		seen := make(map[string]bool, len(dirs))
		for _, dir := range dirs {
			if filepath.Dir(dir) != shared {
				t.Errorf("script coverage directory %s is not inside %s", dir, shared)
			}
			if seen[dir] {
				t.Errorf("two scripts share the coverage directory %s", dir)
			}
			seen[dir] = true
		}
		collected := countCoverCounters(t, shared) - countersBefore
		if collected < len(scripts) {
			t.Errorf("collected %d counter files into %s, want at least %d", collected, shared, len(scripts))
		}
	})

	testscript.Run(t, testscript.Params{
		Dir:                 scriptDir,
		RequireExplicitExec: true,
		Setup:               setupScript,
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"coverdir": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) != 0 {
					ts.Fatalf("usage: coverdir")
				}
				mu.Lock()
				defer mu.Unlock()
				dirs = append(dirs, ts.Getenv(coverDirEnv))
			},
		},
	})
}

func countCoverCounters(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), coverCounterPrefix) {
			count++
		}
	}
	return count
}
