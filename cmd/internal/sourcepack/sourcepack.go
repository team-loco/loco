package sourcepack

import (
	"archive/tar"
	"cmp"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

const (
	largestKept    = 5
	ignoreFileName = ".dockerignore"
)

var (
	ErrDockerfileMissing = errors.New("dockerfile not found in the build context")
	ErrNoDockerfiles     = errors.New("no dockerfile to pack")
)

type File struct {
	Path string
	Size int64
}
type Summary struct {
	Files      int
	InputBytes int64
	Largest    []File
}
type Archive struct {
	Summary

	Path string
	Size int64
}

func (a *Archive) Remove() error {
	return os.Remove(a.Path)
}
func Pack(dir string, dockerfiles []string) (*Archive, error) {
	tmp, err := os.CreateTemp("", "loco-source-*.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("create archive file: %w", err)
	}
	tmpPath := tmp.Name()
	archive := &Archive{Path: tmpPath}
	summary, writeErr := Write(tmp, dir, dockerfiles)
	closeErr := tmp.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		if removeErr := os.Remove(tmpPath); removeErr != nil {
			writeErr = errors.Join(writeErr, removeErr)
		}
		return nil, writeErr
	}
	info, err := os.Stat(archive.Path)
	if err != nil {
		return nil, fmt.Errorf("stat archive: %w", err)
	}
	archive.Summary = *summary
	archive.Size = info.Size()
	return archive, nil
}
func Write(w io.Writer, dir string, dockerfiles []string) (*Summary, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve build context %s: %w", dir, err)
	}
	if len(dockerfiles) == 0 {
		return nil, ErrNoDockerfiles
	}
	keep := []string{ignoreFileName}
	for _, dockerfile := range dockerfiles {
		cleaned, pathErr := contextPath(dockerfile)
		if pathErr != nil {
			return nil, pathErr
		}
		dockerfileOnDisk := contextFile(root, cleaned)
		if !isRegularFile(dockerfileOnDisk) {
			return nil, fmt.Errorf("%w: %s in %s", ErrDockerfileMissing, cleaned, root)
		}
		keep = append(keep, cleaned)
	}

	patterns, err := readIgnoreFile(root)
	if err != nil {
		return nil, err
	}
	matcher, err := patternmatcher.New(patterns)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", ignoreFileName, err)
	}

	gzipWriter := gzip.NewWriter(w)
	p := &packer{
		root:       root,
		matcher:    matcher,
		keep:       keep,
		summary:    &Summary{},
		gzipWriter: gzipWriter,
	}
	p.tarWriter = tar.NewWriter(p.gzipWriter)
	if walkErr := filepath.WalkDir(root, p.visit); walkErr != nil {
		return nil, walkErr
	}
	if closeErr := p.tarWriter.Close(); closeErr != nil {
		return nil, fmt.Errorf("finish archive: %w", closeErr)
	}
	if closeErr := p.gzipWriter.Close(); closeErr != nil {
		return nil, fmt.Errorf("finish archive: %w", closeErr)
	}
	return p.summary, nil
}
func AlwaysExcluded(name string) bool {
	return name == ".git" || name == ".env" || strings.HasPrefix(name, ".env.")
}

type packer struct {
	root       string
	matcher    *patternmatcher.PatternMatcher
	keep       []string
	summary    *Summary
	gzipWriter *gzip.Writer
	tarWriter  *tar.Writer
}

func (p *packer) visit(file string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return fmt.Errorf("read %s: %w", file, walkErr)
	}
	if file == p.root {
		return nil
	}
	rel, err := filepath.Rel(p.root, file)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", file, err)
	}
	name := filepath.ToSlash(rel)

	base := entry.Name()
	if AlwaysExcluded(base) {
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}

	ignored, err := p.ignored(name)
	if err != nil {
		return err
	}
	if ignored {
		if entry.IsDir() && !p.matcher.Exclusions() && !p.holdsKept(name) {
			return filepath.SkipDir
		}
		return nil
	}

	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("stat %s: %w", file, err)
	}
	return p.add(file, name, info)
}

func (p *packer) holdsKept(dir string) bool {
	prefix := dir + "/"
	for _, kept := range p.keep {
		if strings.HasPrefix(kept, prefix) {
			return true
		}
	}
	return false
}

func (p *packer) ignored(name string) (bool, error) {
	if slices.Contains(p.keep, name) {
		return false, nil
	}
	ignored, err := p.matcher.MatchesOrParentMatches(name)
	if err != nil {
		return false, fmt.Errorf("match %s against the ignore patterns: %w", name, err)
	}
	return ignored, nil
}

func (p *packer) add(file, name string, info fs.FileInfo) error {
	mode := info.Mode()
	link := ""
	if mode&fs.ModeSymlink != 0 {
		target, err := os.Readlink(file)
		if err != nil {
			return fmt.Errorf("read link %s: %w", file, err)
		}
		link = filepath.ToSlash(target)
	} else if !mode.IsRegular() && !mode.IsDir() {
		return nil
	}

	header, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return fmt.Errorf("archive header for %s: %w", name, err)
	}
	header.Name = name
	if mode.IsDir() {
		header.Name += "/"
	}
	header.Uid = 0
	header.Gid = 0
	header.Uname = ""
	header.Gname = ""
	header.Format = tar.FormatPAX
	if writeErr := p.tarWriter.WriteHeader(header); writeErr != nil {
		return fmt.Errorf("write %s: %w", name, writeErr)
	}
	if !mode.IsRegular() {
		return nil
	}
	size := info.Size()
	return p.copyFile(file, name, size)
}

func (p *packer) copyFile(file, name string, size int64) error {
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("open %s: %w", file, err)
	}
	defer f.Close()
	if _, copyErr := io.CopyN(p.tarWriter, f, size); copyErr != nil {
		return fmt.Errorf("write %s: %w", name, copyErr)
	}
	p.summary.Files++
	p.summary.InputBytes += size
	p.summary.Largest = keepLargest(p.summary.Largest, File{Path: name, Size: size})
	return nil
}

func keepLargest(files []File, file File) []File {
	files = append(files, file)
	slices.SortStableFunc(files, func(a, b File) int {
		return cmp.Compare(b.Size, a.Size)
	})
	if len(files) > largestKept {
		files = files[:largestKept]
	}
	return files
}

func contextPath(dockerfile string) (string, error) {
	slashed := filepath.ToSlash(dockerfile)
	cleaned := path.Clean(slashed)
	if cleaned == "." || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("dockerfile path %q must be inside the build context", dockerfile)
	}
	return cleaned, nil
}

func readIgnoreFile(root string) ([]string, error) {
	ignorePath := contextFile(root, ignoreFileName)
	f, err := os.Open(ignorePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", ignoreFileName, err)
	}
	patterns, readErr := ignorefile.ReadAll(f)
	closeErr := f.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read %s: %w", ignoreFileName, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close %s: %w", ignoreFileName, closeErr)
	}
	return patterns, nil
}

func contextFile(root, name string) string {
	native := filepath.FromSlash(name)
	return filepath.Join(root, native)
}

func isRegularFile(file string) bool {
	info, err := os.Stat(file)
	return err == nil && info.Mode().IsRegular()
}
