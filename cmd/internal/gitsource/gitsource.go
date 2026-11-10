// Package gitsource reads the git commit a source directory is checked out at.
package gitsource

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Commit is the commit a directory is checked out at. The zero Commit means the directory
// is not inside a git work tree, the work tree has no commits, or git is not installed.
type Commit struct {
	SHA string
	// Branch is empty on a detached HEAD.
	Branch string
	// Message is the commit's subject line.
	Message string
}

func Read(ctx context.Context, dir string) (Commit, error) {
	head, err := git(ctx, dir, "log", "-1", "--format=%H%n%s")
	// git exits non-zero outside a work tree, in one without commits and in one it cannot
	// read; telling them apart means parsing localized messages, and a build needs none of
	// them, so all read as no commit.
	_, exited := errors.AsType[*exec.ExitError](err)
	if exited || errors.Is(err, exec.ErrNotFound) {
		return Commit{}, nil
	}
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
	return Commit{SHA: sha, Branch: strings.TrimSuffix(branch, "\n"), Message: subject}, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %w", args[0], dir, err)
	}
	return string(out), nil
}
