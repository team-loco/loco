package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	files, err := exec.CommandContext(
		context.Background(), "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard",
	).
		Output()
	if err != nil {
		return err
	}
	result := map[string]string{}
	for _, argument := range os.Args[1:] {
		name, dockerfile, ok := strings.Cut(argument, "=")
		if !ok || name == "" || dockerfile == "" {
			return fmt.Errorf("expected image=Dockerfile, got %q", argument)
		}
		sum, err := fingerprint(".", dockerfile, strings.Split(strings.TrimSuffix(string(files), "\x00"), "\x00"))
		if err != nil {
			return err
		}
		result[name] = sum
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func fingerprint(root, dockerfile string, files []string) (string, error) {
	ignorePath := dockerfile + ".dockerignore"
	// #nosec G703 -- Dockerfile paths are explicit local build inputs.
	data, err := os.ReadFile(filepath.Join(root, ignorePath))
	if os.IsNotExist(err) {
		ignorePath = ".dockerignore"
		data, err = os.ReadFile(filepath.Join(root, ignorePath))
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	patterns, err := ignorefile.ReadAll(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	matcher, err := patternmatcher.New(patterns)
	if err != nil {
		return "", err
	}
	files = slices.Clone(files)
	slices.Sort(files)
	files = slices.Compact(files)
	hash := sha256.New()
	if _, err = fmt.Fprintf(hash, "%s\x00%s\x00", dockerfile, data); err != nil {
		return "", err
	}
	for _, path := range files {
		excluded, matchErr := matcher.MatchesOrParentMatches(path)
		if matchErr != nil {
			return "", matchErr
		}
		if excluded && path != dockerfile {
			continue
		}
		info, statErr := os.Lstat(filepath.Join(root, path))
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return "", statErr
		}
		if info.IsDir() {
			continue
		}
		var content []byte
		if info.Mode()&fs.ModeSymlink != 0 {
			target, linkErr := os.Readlink(filepath.Join(root, path))
			if linkErr != nil {
				return "", linkErr
			}
			content = []byte(target)
		} else {
			content, err = os.ReadFile(filepath.Join(root, path))
			if err != nil {
				return "", err
			}
		}
		if _, err = fmt.Fprintf(hash, "%s\x00%d\x00%s\x00", path, info.Mode(), content); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
