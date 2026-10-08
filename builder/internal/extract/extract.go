package extract

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

const (
	maxNameLength    = 4096
	maxTrailingBytes = 1 << 20
	filePerm         = 0o644
	execPerm         = 0o755
	dirPerm          = 0o755
)

var ErrLimitExceeded = errors.New("limit exceeded")

type Limits struct {
	MaxBytes   int64
	MaxEntries int
}

type extractor struct {
	root    *os.Root
	limits  Limits
	written int64
	entries int
}

func Archive(r io.Reader, dest string, limits Limits) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	root, err := os.OpenRoot(dest)
	if err != nil {
		return fmt.Errorf("open %s: %w", dest, err)
	}
	defer root.Close()

	e := &extractor{root: root, limits: limits}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if err := e.entry(hdr, tr); err != nil {
			return err
		}
	}
	trailing, drainErr := io.CopyN(io.Discard, gz, maxTrailingBytes+1)
	if drainErr != nil && !errors.Is(drainErr, io.EOF) {
		return fmt.Errorf("read archive trailer: %w", drainErr)
	}
	if trailing > maxTrailingBytes {
		return fmt.Errorf("archive trailer: %w", ErrLimitExceeded)
	}
	return checkLinks(root)
}

func cleanName(name string) (string, error) {
	if name == "" || len(name) > maxNameLength {
		return "", fmt.Errorf("invalid entry name %q", name)
	}
	if strings.ContainsRune(name, 0) || strings.Contains(name, `\`) {
		return "", fmt.Errorf("invalid entry name %q", name)
	}
	if path.IsAbs(name) {
		return "", fmt.Errorf("entry %q has an absolute path", name)
	}
	for part := range strings.SplitSeq(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("entry %q leaves the build context", name)
		}
	}
	return path.Clean(name), nil
}

func (e *extractor) entry(hdr *tar.Header, body io.Reader) error {
	e.entries++
	if e.limits.MaxEntries > 0 && e.entries > e.limits.MaxEntries {
		return fmt.Errorf("archive has more than %d entries: %w", e.limits.MaxEntries, ErrLimitExceeded)
	}
	name, err := cleanName(hdr.Name)
	if err != nil {
		return err
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		return e.dir(name, hdr)
	case tar.TypeReg:
		return e.file(name, hdr, body)
	case tar.TypeSymlink:
		return e.symlink(name, hdr.Linkname)
	case tar.TypeLink:
		return e.hardlink(name, hdr.Linkname)
	case tar.TypeXGlobalHeader:
		return nil
	default:
		return fmt.Errorf("entry %q has unsupported type %q", hdr.Name, hdr.Typeflag)
	}
}

func (e *extractor) dir(name string, hdr *tar.Header) error {
	if name == "." {
		return nil
	}
	perm := hdr.FileInfo().Mode().Perm()&execPerm | 0o700
	if err := e.root.MkdirAll(name, perm); err != nil {
		return fmt.Errorf("create directory %q: %w", name, err)
	}
	return nil
}

func (e *extractor) parent(name string) error {
	dir := path.Dir(name)
	if dir == "." {
		return nil
	}
	if err := e.root.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create directory %q: %w", dir, err)
	}
	return nil
}

func (e *extractor) replace(name string) error {
	info, err := e.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %q: %w", name, err)
	}
	if info.IsDir() {
		return fmt.Errorf("entry %q replaces a directory", name)
	}
	if err := e.root.Remove(name); err != nil {
		return fmt.Errorf("replace %q: %w", name, err)
	}
	return nil
}

func (e *extractor) file(name string, hdr *tar.Header, body io.Reader) error {
	if name == "." {
		return fmt.Errorf("entry %q is not a file name", hdr.Name)
	}
	if hdr.Size < 0 {
		return fmt.Errorf("entry %q has a negative size", hdr.Name)
	}
	e.written += hdr.Size
	if e.limits.MaxBytes > 0 && e.written > e.limits.MaxBytes {
		return fmt.Errorf("build context is larger than %d bytes: %w", e.limits.MaxBytes, ErrLimitExceeded)
	}
	if err := e.parent(name); err != nil {
		return err
	}
	if err := e.replace(name); err != nil {
		return err
	}
	perm := os.FileMode(filePerm)
	if hdr.FileInfo().Mode().Perm()&0o111 != 0 {
		perm = execPerm
	}
	f, err := e.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("create %q: %w", name, err)
	}
	n, copyErr := io.Copy(f, io.LimitReader(body, hdr.Size))
	closeErr := f.Close()
	if copyErr != nil {
		return fmt.Errorf("write %q: %w", name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %q: %w", name, closeErr)
	}
	if n != hdr.Size {
		return fmt.Errorf("entry %q is truncated", name)
	}
	return nil
}

func insideRoot(name string) bool {
	return name != ".." && !strings.HasPrefix(name, "../") && !path.IsAbs(name)
}

func (e *extractor) symlink(name, target string) error {
	if name == "." {
		return fmt.Errorf("symlink %q replaces the build context", name)
	}
	if target == "" || strings.ContainsRune(target, 0) || path.IsAbs(target) {
		return fmt.Errorf("symlink %q has an absolute or empty target %q", name, target)
	}
	dir := path.Dir(name)
	resolved := path.Join(dir, target)
	if !insideRoot(resolved) {
		return fmt.Errorf("symlink %q points outside the build context", name)
	}
	if err := e.parent(name); err != nil {
		return err
	}
	if err := e.replace(name); err != nil {
		return err
	}
	if err := e.root.Symlink(target, name); err != nil {
		return fmt.Errorf("create symlink %q: %w", name, err)
	}
	return nil
}

func (e *extractor) hardlink(name, target string) error {
	if name == "." {
		return fmt.Errorf("hard link %q replaces the build context", name)
	}
	cleanTarget, err := cleanName(target)
	if err != nil {
		return fmt.Errorf("hard link %q: %w", name, err)
	}
	info, err := e.root.Lstat(cleanTarget)
	if err != nil {
		return fmt.Errorf("hard link %q: target %q: %w", name, target, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("hard link %q targets %q, which is not a regular file", name, target)
	}
	if err := e.parent(name); err != nil {
		return err
	}
	if err := e.replace(name); err != nil {
		return err
	}
	if err := e.root.Link(cleanTarget, name); err != nil {
		return fmt.Errorf("create hard link %q: %w", name, err)
	}
	return nil
}

func checkLinks(root *os.Root) error {
	fsys := root.FS()
	return fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %q: %w", name, err)
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		_, statErr := root.Stat(name)
		if statErr == nil || errors.Is(statErr, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("symlink %q points outside the build context", name)
	})
}
