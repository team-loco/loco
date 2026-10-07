package infra

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

func sourceRoot(ctx context.Context, projectRoot string) string {
	command := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	command.Dir = projectRoot
	data, err := command.Output()
	if err != nil {
		return projectRoot
	}
	return strings.TrimSpace(string(data))
}

func validateLocalDependencies(ctx context.Context, projectRoot string, reviewed bool) error {
	return validateModuleDependencies(ctx, projectRoot, filepath.Join(projectRoot, ".loco"), reviewed)
}

func validateModuleDependencies(ctx context.Context, projectRoot, moduleRoot string, reviewed bool) error {
	root := sourceRoot(ctx, projectRoot)
	data, err := os.ReadFile(filepath.Join(moduleRoot, "go.mod"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	module, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return err
	}
	for _, replacement := range module.Replace {
		if replacement.New.Version != "" {
			continue
		}
		path := replacement.New.Path
		if filepath.IsAbs(path) && reviewed {
			return fmt.Errorf("reviewed definitions require relative local module replacements")
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(moduleRoot, path)
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve local module dependency: %w", err)
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil || !withinSourceRoot(relative) {
			return fmt.Errorf("local module replacements cannot escape the source root")
		}
	}
	return nil
}

func Snapshot(ctx context.Context, module *Definition) (*Definition, func(), error) {
	root := sourceRoot(ctx, module.ProjectRoot)
	command := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached")
	command.Dir = root
	data, err := command.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("snapshot tracked source: %w", err)
	}
	directory, err := os.MkdirTemp("", "loco-reviewed-source-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		if removeErr := os.RemoveAll(directory); removeErr != nil {
			slog.WarnContext(ctx, "remove source snapshot", "error", removeErr)
		}
	}
	for _, path := range strings.Split(string(data), "\x00") {
		if path == "" {
			continue
		}
		if copyErr := copySourceFile(
			filepath.Join(root, filepath.FromSlash(path)),
			filepath.Join(directory, filepath.FromSlash(path)),
		); copyErr != nil {
			cleanup()
			return nil, nil, copyErr
		}
	}
	relative, err := filepath.Rel(root, module.ProjectRoot)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	entrypoint, pathErr := filepath.Rel(root, module.File)
	if pathErr != nil || !withinSourceRoot(entrypoint) {
		cleanup()
		return nil, nil, fmt.Errorf("definition cannot escape source root")
	}
	snapshot, err := Discover(
		filepath.Join(directory, relative),
		filepath.Join(directory, entrypoint),
		filepath.Join(directory, relative),
	)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return snapshot, cleanup, nil
}

func copySourceFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("reviewed source must contain regular files")
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func ValidateSourceDefinition(ctx context.Context, module *Definition, reviewed bool) error {
	root := sourceRoot(ctx, module.ProjectRoot)
	for _, path := range []string{module.File, module.ModuleRoot} {
		relative, err := filepath.Rel(root, path)
		if err != nil || !withinSourceRoot(relative) {
			return fmt.Errorf("infrastructure definition must be inside the source root")
		}
	}
	return validateModuleDependencies(ctx, module.ProjectRoot, module.ModuleRoot, reviewed)
}
