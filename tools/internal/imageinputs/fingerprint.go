// Package imageinputs fingerprints the build inputs of a Docker image.
package imageinputs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// Fingerprint hashes the Dockerfile, its ignore rules and every file in files that enters its build context.
func Fingerprint(root, dockerfile string, files []string) (string, error) {
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
