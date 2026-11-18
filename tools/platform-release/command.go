package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/semver"
)

var (
	versionPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-rc\.(0|[1-9]\d*))?$`)
	shaPattern     = regexp.MustCompile(`^[a-f0-9]{40}$`)
	digestPattern  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

type tool struct {
	root string
	ctx  context.Context
}

func (t *tool) run(args ...string) (string, error) {
	cmd, err := t.command(args...)
	if err != nil {
		return "", err
	}
	cmd.Dir = t.root
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (t *tool) command(args ...string) (*exec.Cmd, error) {
	if len(args) == 0 {
		return nil, errors.New("missing release command")
	}
	arguments := args[1:]
	switch args[0] {
	case "git", "go", "helm", "mise", dockerCommand, "gh":
		// #nosec G702 -- The operator supplies argv; executable names are allowlisted and no shell is used.
		return exec.CommandContext(t.ctx, args[0], arguments...), nil
	default:
		return nil, fmt.Errorf("unsupported release command: %s", args[0])
	}
}

func (t *tool) value(path, key, ref string) (any, error) {
	var data []byte
	if ref == "" {
		var err error
		data, err = os.ReadFile(filepath.Join(t.root, path))
		if err != nil {
			return nil, err
		}
	} else {
		found, err := t.run("git", "ls-tree", "--name-only", ref, "--", path)
		if err != nil {
			return nil, err
		}
		if found == "" {
			return nil, nil
		}
		out, err := t.run("git", "show", ref+":"+path)
		if err != nil {
			return nil, err
		}
		data = []byte(out)
	}
	var document object
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if key == "" {
		return document, nil
	}
	return document[key], nil
}

func validateVersion(version string) error {
	if !versionPattern.MatchString(version) {
		return fmt.Errorf("version must be X.Y.Z or X.Y.Z-rc.N without a v prefix: %s", version)
	}
	return nil
}

func compareVersions(a, b string) (int, error) {
	if checkErr := validateVersion(a); checkErr != nil {
		return 0, checkErr
	}
	if checkErr := validateVersion(b); checkErr != nil {
		return 0, checkErr
	}
	return semver.Compare("v"+a, "v"+b), nil
}

func (t *tool) validateCommit(commit string) error {
	if !shaPattern.MatchString(commit) {
		return errors.New("core commit must be a full lowercase Git SHA")
	}
	head, err := t.run("git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != commit {
		return errors.New("checked-out source differs from core commit")
	}
	_, err = t.run("git", "merge-base", "--is-ancestor", commit, "origin/main")
	return err
}
