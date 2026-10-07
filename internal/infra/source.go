package infra

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

func SourceDigest(ctx context.Context, projectRoot string, excluded []string, reviewed bool) (string, error) {
	canonicalRoot, rootErr := filepath.EvalSymlinks(projectRoot)
	if rootErr != nil {
		return "", rootErr
	}
	projectRoot = canonicalRoot
	if err := validateLocalDependencies(ctx, projectRoot, reviewed); err != nil {
		return "", err
	}
	root := projectRoot
	command := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	command.Dir = projectRoot
	gitRoot, gitErr := command.Output()
	var paths []string
	if gitErr == nil {
		root = strings.TrimSpace(string(gitRoot))
		args := []string{"ls-files", "-z", "--cached"}
		if !reviewed {
			args = append(args, "--others", "--exclude-standard")
		}
		command = exec.CommandContext(ctx, "git", args...)
		command.Dir = root
		output, err := command.Output()
		if err != nil {
			return "", fmt.Errorf("list source inputs: %w", err)
		}
		for _, path := range strings.Split(string(output), "\x00") {
			if path != "" {
				paths = append(paths, path)
			}
		}
		if reviewed {
			command = exec.CommandContext(ctx, "git", "diff", "--exit-code", "HEAD", "--")
			command.Dir = root
			if runErr := command.Run(); runErr != nil {
				return "", fmt.Errorf("reviewed plans require a clean tracked source tree")
			}
		}
	} else {
		if reviewed {
			return "", fmt.Errorf("reviewed plans require a Git repository")
		}
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, parameterErr error) error {
			if parameterErr != nil {
				return parameterErr
			}
			if entry.IsDir() && excludedDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".env") {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				paths = append(paths, filepath.ToSlash(rel))
			}
			return nil
		}); err != nil {
			return "", fmt.Errorf("list source files: %w", err)
		}
	}
	ignored := make([]string, 0, len(excluded)+1)
	for _, path := range append([]string{".loco/link.json"}, excluded...) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(projectRoot, filepath.FromSlash(path))
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		if withinSourceRoot(rel) {
			ignored = append(ignored, filepath.ToSlash(rel))
		}
	}
	if reviewed {
		if err := reviewedInputs(ctx, root, ignored); err != nil {
			return "", err
		}
	}
	slices.Sort(ignored)
	slices.Sort(paths)
	paths = slices.Compact(paths)
	hash := sha256.New()
	if _, err := fmt.Fprintf(hash, "loco-source-v1\n%v\n", ignored); err != nil {
		return "", err
	}
	for _, path := range paths {
		if slices.ContainsFunc(ignored, func(excluded string) bool {
			return path == excluded || strings.HasPrefix(path, strings.TrimSuffix(excluded, "/")+"/")
		}) {
			continue
		}
		if err := hashSourceFile(hash, root, path, reviewed); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func excludedDirectory(name string) bool {
	switch name {
	case ".git":
		return true
	case "node_modules":
		return true
	case ".cache":
		return true
	default:
		return false
	}
}

func withinSourceRoot(relative string) bool {
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func reviewedInputs(ctx context.Context, root string, excluded []string) error {
	command := exec.CommandContext(ctx, "git", "ls-files", "-z", "--others", "--exclude-standard")
	command.Dir = root
	data, err := command.Output()
	if err != nil {
		return err
	}
	for _, path := range strings.Split(string(data), "\x00") {
		if path == "" || slices.ContainsFunc(excluded, func(ignored string) bool {
			return path == ignored || strings.HasPrefix(path, strings.TrimSuffix(ignored, "/")+"/")
		}) {
			continue
		}
		return fmt.Errorf("reviewed source contains untracked input %q", path)
	}
	return nil
}

func hashSourceFile(hash io.Writer, root, path string, reviewed bool) error {
	full := filepath.Join(root, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if err != nil {
		return fmt.Errorf("read source input: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf(
			"source input %q must be a regular file; symlinks and submodules are unsupported",
			path,
		)
	}
	if _, fprintfErr := fmt.Fprintf(
		hash,
		"%d:%s:%d:%d\n",
		len(path),
		path,
		info.Mode().Perm()&0o111,
		info.Size(),
	); fprintfErr != nil {
		return fprintfErr
	}
	file, err := os.Open(full)
	if err != nil {
		return err
	}
	input := bufio.NewReader(file)
	prefix, peekErr := input.Peek(64)
	if peekErr != nil && !errors.Is(peekErr, io.EOF) {
		if closeErr := file.Close(); closeErr != nil {
			return closeErr
		}
		return peekErr
	}
	if reviewed && bytes.HasPrefix(prefix, []byte("version https://git-lfs.github.com/spec/v1")) {
		if closeErr := file.Close(); closeErr != nil {
			return closeErr
		}
		return fmt.Errorf("reviewed source contains unresolved LFS content")
	}
	_, copyErr := io.Copy(hash, input)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}
