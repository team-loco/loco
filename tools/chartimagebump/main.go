// Command chartimagebump bumps each chart affected by image updates once.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
)

var (
	chartValues = regexp.MustCompile(`^charts/[a-z0-9-]+/values\.yaml$`)
	versionLine = regexp.MustCompile(`(?m)^version: ([0-9]+\.[0-9]+\.)([0-9]+)$`)
	errVersion  = errors.New("expected a chart version in major.minor.patch format")
)

func main() {
	if err := bumpCharts(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func bumpCharts(ctx context.Context, paths []string) error {
	seen := make(map[string]bool)
	for _, path := range paths {
		if !chartValues.MatchString(path) || seen[path] {
			continue
		}
		seen[path] = true
		chart := filepath.Join(filepath.Dir(path), "Chart.yaml")
		if err := bumpChart(ctx, chart); err != nil {
			return fmt.Errorf("%s: %w", chart, err)
		}
	}
	return nil
}

func bumpChart(ctx context.Context, path string) error {
	//nolint:gosec // chartValues validates path.
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	//nolint:gosec // chartValues validates path.
	base, err := exec.CommandContext(ctx, "git", "show", "HEAD:"+filepath.ToSlash(path)).Output()
	if err != nil {
		return fmt.Errorf("read committed chart: %w", err)
	}
	version := versionLine.FindSubmatch(current)
	committed := versionLine.FindSubmatch(base)
	if version == nil || committed == nil {
		return errVersion
	}
	if !bytes.Equal(version[0], committed[0]) {
		return nil
	}
	patch, err := strconv.Atoi(string(version[2]))
	if err != nil {
		return err
	}
	next := []byte(fmt.Sprintf("version: %s%d", version[1], patch+1))
	//nolint:gosec // chartValues validates path.
	return os.WriteFile(path, versionLine.ReplaceAllLiteral(current, next), 0o600)
}
