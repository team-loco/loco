package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"sync"
)

const markerPrefix = "ci-pass-"

// markerInputs are inputs of every workflow beyond its own paths.
var markerInputs = []string{"mise.lock", ".github/workflows/" + ciWorkflowFile}

const ciWorkflowFile = "ci.yml"

// markerLookup reports whether a cache entry with exactly this key exists.
type markerLookup func(ctx context.Context, key string) (bool, error)

// markerKeys returns, for each selected workflow that may reuse a pass, the
// cache key of a pass on the checked-out tree: a hash of the workflow name and
// the tree entries of its pull request paths, its workflow file and markerInputs.
func markerKeys(ctx context.Context, dir string, all filters, selected map[string]bool) (map[string]string, error) {
	keys := map[string]string{}
	var entries []treeEntry
	for workflow, w := range all {
		if !selected[workflow] || !w.reuse {
			continue
		}
		if entries == nil {
			var err error
			if entries, err = tree(ctx, dir); err != nil {
				return nil, err
			}
		}
		keys[workflow] = markerKey(workflow, w.events["pull_request"], entries)
	}
	return keys, nil
}

type treeEntry struct {
	path string
	line string
}

func tree(ctx context.Context, dir string) ([]treeEntry, error) {
	out, err := git(ctx, dir, "ls-tree", "-r", "--full-tree", "-z", "HEAD")
	if err != nil {
		return nil, err
	}
	var entries []treeEntry
	for line := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		_, path, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("git ls-tree: unexpected entry %q", line)
		}
		entries = append(entries, treeEntry{path: path, line: line})
	}
	return entries, nil
}

func markerKey(workflow string, inputs filter, entries []treeEntry) string {
	workflowFile := ".github/workflows/" + workflow + ".yml"
	digest := sha256.New()
	fmt.Fprintf(digest, "%s\n", workflow)
	for _, entry := range entries {
		if entry.path == workflowFile || slices.Contains(markerInputs, entry.path) || inputs.matches(entry.path) {
			fmt.Fprintf(digest, "%s\n", entry.line)
		}
	}
	return markerPrefix + workflow + "-" + hex.EncodeToString(digest.Sum(nil))
}

// passed returns the workflows whose marker the lookup finds. A failed lookup
// counts as no marker, so the workflow runs.
func passed(ctx context.Context, lookup markerLookup, keys map[string]string) []string {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		result []string
	)
	for workflow, key := range keys {
		wg.Go(func() {
			found, err := lookup(ctx, key)
			if err != nil {
				slog.Warn("look up a pass marker", "workflow", workflow, "error", err)
				return
			}
			if found {
				mu.Lock()
				result = append(result, workflow)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	slices.Sort(result)
	return result
}

type cacheEntry struct {
	Key string `json:"key"`
}

// cacheLookup finds a key among the caches of refs, the refs whose caches
// this run could restore. gh reads the repository from GH_REPO and the token
// from GH_TOKEN.
func cacheLookup(refs []string) markerLookup {
	return func(ctx context.Context, key string) (bool, error) {
		for _, ref := range refs {
			cmd := exec.CommandContext(ctx, "gh", "cache", "list", "--key", key, "--ref", ref, "--json", "key")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				return false, fmt.Errorf("gh cache list --ref %s: %w: %s", ref, err, stderr.String())
			}
			var caches []cacheEntry
			if err := json.Unmarshal(out, &caches); err != nil {
				return false, fmt.Errorf("gh cache list --ref %s: %w", ref, err)
			}
			// --key matches by prefix.
			if slices.ContainsFunc(caches, func(c cacheEntry) bool { return c.Key == key }) {
				return true, nil
			}
		}
		return false, nil
	}
}
