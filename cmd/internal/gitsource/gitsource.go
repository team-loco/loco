// Package gitsource reads the git commit a source directory is checked out at.
package gitsource

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Commit is the commit a directory is checked out at. The zero Commit means the directory
// is not inside a git work tree, the work tree has no commits, or git is not installed.
type Commit struct {
	SHA   string
	Dirty bool
	// Branch is empty on a detached HEAD.
	Branch string
	// Message is the commit's subject line.
	Message string
}

func Read(ctx context.Context, dir string) (Commit, error) {
	inside, err := git(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if errors.Is(err, exec.ErrNotFound) {
		return Commit{}, nil
	}
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 128 {
			exists, statErr := repositoryMarker(dir)
			if statErr != nil {
				return Commit{}, statErr
			}
			if !exists {
				return Commit{}, nil
			}
		}
		return Commit{}, err
	}
	if strings.TrimSpace(inside) != "true" {
		return Commit{}, nil
	}
	reference, err := git(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
	if err == nil {
		_, err = git(ctx, dir, "show-ref", "--exists", strings.TrimSpace(reference))
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 2 {
			return Commit{}, nil
		}
	} else if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 1 {
		err = nil
	}
	if err != nil {
		return Commit{}, err
	}
	head, err := git(ctx, dir, "log", "-1", "--format=%H%n%s")
	if err != nil {
		return Commit{}, err
	}
	sha, subject, _ := strings.Cut(strings.TrimSuffix(head, "\n"), "\n")

	branch, err := git(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	// With --quiet, exit status 1 means HEAD is detached and names no branch.
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 1 {
		err = nil
	}
	if err != nil {
		return Commit{}, err
	}
	status, err := git(ctx, dir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return Commit{}, err
	}
	return Commit{SHA: sha, Branch: strings.TrimSuffix(branch, "\n"), Message: subject, Dirty: status != ""}, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %w", args[0], dir, err)
	}
	return string(out), nil
}

// Git also reports a broken HEAD as a non-repository; retain errors for repository storage.
func repositoryMarker(dir string) (bool, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return false, fmt.Errorf("resolve source directory: %w", err)
	}
	for {
		marker := filepath.Join(dir, ".git")
		info, err := os.Lstat(marker)
		if err == nil {
			if !info.IsDir() {
				return true, nil
			}
			for _, name := range []string{"objects", "refs", "config"} {
				if _, statErr := os.Stat(filepath.Join(marker, name)); statErr == nil {
					return true, nil
				} else if !errors.Is(statErr, os.ErrNotExist) {
					return false, fmt.Errorf("inspect repository storage: %w", statErr)
				}
			}
			return false, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("inspect repository in %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, nil
		}
		dir = parent
	}
}
