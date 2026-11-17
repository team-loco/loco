package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// releases uses the head's bundle tooling for both chart generations. See #2186
// for selecting a published baseline after the first bundle v2 release.
func (f *fixture) releases(ctx context.Context) (string, string, error) {
	if err := f.pullPrebuilt(ctx); err != nil {
		return "", "", err
	}
	base, err := command(ctx, f.root, nil, "git", "merge-base", "HEAD", "origin/main")
	if err != nil {
		return "", "", err
	}
	f.baselineRoot = filepath.Join(filepath.Dir(f.root), f.name+"-baseline")
	if _, err = os.Stat(f.baselineRoot); os.IsNotExist(err) {
		if _, err = command(
			ctx,
			f.root,
			nil,
			"git",
			"worktree",
			"add",
			"--detach",
			f.baselineRoot,
			strings.TrimSpace(string(base)),
		); err != nil {
			return "", "", err
		}
	} else if err != nil {
		return "", "", err
	}
	actual, err := command(ctx, f.baselineRoot, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(string(actual)) != strings.TrimSpace(string(base)) {
		return "", "", errors.New("baseline checkout differs from merge base")
	}
	if local, readErr := os.ReadFile(filepath.Join(f.root, "CLAUDE.local.md")); readErr == nil {
		// #nosec G703 -- baselineRoot is the checkout-adjacent fixture worktree created above.
		if err = os.WriteFile(filepath.Join(f.baselineRoot, "CLAUDE.local.md"), local, 0600); err != nil {
			return "", "", err
		}
	} else if !os.IsNotExist(readErr) {
		return "", "", readErr
	}
	for _, args := range [][]string{{"trust"}, {"run", "setup"}} {
		if _, err = command(ctx, f.baselineRoot, nil, "mise", args...); err != nil {
			return "", "", err
		}
	}
	previous := *f
	previous.dir, previous.images = filepath.Join(f.dir, "baseline"), ""
	if err = os.MkdirAll(previous.dir, 0700); err != nil {
		return "", "", err
	}
	fmt.Println("Publishing merge-base charts and images with current bundle tooling")
	oldRef, err := previous.release(ctx, f.baselineRoot)
	if err != nil {
		return "", "", err
	}
	fmt.Println("Publishing head charts and images")
	newRef, err := f.release(ctx, f.root)
	if err != nil {
		return "", "", err
	}
	if err = f.buildLocoadm(ctx, f.version); err != nil {
		return "", "", err
	}
	return oldRef, newRef, nil
}
