package testres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

var errBadPID = errors.New("process resource needs a positive process ID")

func removeResource(ctx context.Context, res resource) error {
	switch res.Kind {
	case Container:
		err := command(ctx, "docker", "rm", "-f", "-v", res.Name)
		if err != nil && (strings.Contains(err.Error(), "is already in progress") ||
			strings.Contains(err.Error(), "No such container")) {
			return nil
		}
		return err
	case Compose:
		return command(ctx, "docker", "compose", "-p", res.Name, "down", "-v", "--remove-orphans")
	case KindCluster:
		return command(ctx, "kind", "delete", "cluster", "--name", res.Name)
	case Worktree:
		return removeWorktree(ctx, res.Name)
	case Process:
		return stopProcess(ctx, res)
	default:
		return fmt.Errorf("unknown resource kind %q", res.Kind)
	}
}

func removeWorktree(ctx context.Context, path string) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	out, err := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--path-format=absolute", "--git-common-dir").
		Output()
	if err != nil {
		return fmt.Errorf("find repository of worktree %s: %w", path, err)
	}
	return command(ctx, "git", "--git-dir", strings.TrimSpace(string(out)), "worktree", "remove", "--force", path)
}

func stopProcess(ctx context.Context, res resource) error {
	pid, err := parsePID(res.Name)
	if err != nil {
		return err
	}
	started, err := startTime(ctx, pid)
	if err != nil || started != res.Started {
		return err
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop process group %d: %w", pid, err)
	}
	return nil
}

func parsePID(name string) (int, error) {
	pid, err := strconv.Atoi(name)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("%w: %q", errBadPID, name)
	}
	return pid, nil
}

// startTime returns when a process started as ps prints it, or "" when no such process runs.
// Together with the ID it names one process even after the ID is reused.
func startTime(ctx context.Context, pid int) (string, error) {
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	out, err := cmd.Output()
	started := strings.TrimSpace(string(out))
	var exit *exec.ExitError
	if errors.As(err, &exit) && started == "" {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read start time of process %d: %w", pid, err)
	}
	return started, nil
}

func command(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
