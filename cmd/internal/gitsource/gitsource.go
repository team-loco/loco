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
	// Dirty reports whether the work tree differs from the commit, untracked files included.
	Dirty bool
}

// Read returns the commit dir is checked out at.
func Read(ctx context.Context, dir string) (Commit, error) {
	status, err := git(ctx, dir, "status", "--porcelain=v2", "--branch")
	// git exits non-zero both outside a work tree and in one it cannot read; telling them
	// apart means parsing localized messages, and a build needs neither, so both read as
	// no commit.
	_, exited := errors.AsType[*exec.ExitError](err)
	if exited || errors.Is(err, exec.ErrNotFound) {
		return Commit{}, nil
	}
	if err != nil {
		return Commit{}, err
	}

	var commit Commit
	for line := range strings.SplitSeq(strings.TrimSuffix(status, "\n"), "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, "# branch.oid "):
			commit.SHA = strings.TrimPrefix(line, "# branch.oid ")
		case strings.HasPrefix(line, "# branch.head "):
			commit.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# "):
		default:
			commit.Dirty = true
		}
	}
	if commit.SHA == "(initial)" {
		return Commit{}, nil
	}
	if commit.Branch == "(detached)" {
		commit.Branch = ""
	}
	subject, err := git(ctx, dir, "log", "-1", "--format=%s", commit.SHA)
	if err != nil {
		return Commit{}, err
	}
	commit.Message = strings.TrimSuffix(subject, "\n")
	return commit, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	// --no-optional-locks keeps status from rewriting the index while another git command
	// in the same repository holds its lock.
	global := []string{"--no-optional-locks", "-C", dir}
	out, err := exec.CommandContext(ctx, "git", append(global, args...)...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %w", args[0], dir, err)
	}
	return string(out), nil
}
