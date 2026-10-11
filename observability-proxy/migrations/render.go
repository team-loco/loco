package migrations

import (
	"bytes"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"time"
)

const day = 24 * time.Hour

var placeholderPattern = regexp.MustCompile(`\$\{([A-Z_]+)\}`)

func interval(ttl time.Duration) string {
	switch {
	case ttl%day == 0:
		return fmt.Sprintf("toIntervalDay(%d)", ttl/day)
	case ttl%time.Hour == 0:
		return fmt.Sprintf("toIntervalHour(%d)", ttl/time.Hour)
	case ttl%time.Minute == 0:
		return fmt.Sprintf("toIntervalMinute(%d)", ttl/time.Minute)
	default:
		return fmt.Sprintf("toIntervalSecond(%d)", ttl/time.Second)
	}
}

func substitute(content []byte, values map[string]string) ([]byte, error) {
	var unknown []string
	out := placeholderPattern.ReplaceAllFunc(content, func(match []byte) []byte {
		name := string(placeholderPattern.FindSubmatch(match)[1])
		value, ok := values[name]
		if !ok {
			unknown = append(unknown, name)
			return match
		}
		return []byte(value)
	})
	if len(unknown) > 0 {
		return nil, fmt.Errorf("%w: %v", ErrUnknownPlaceholder, unknown)
	}
	return out, nil
}

func render(cfg Config) (fs.FS, error) {
	values := map[string]string{
		"DATABASE":    cfg.Database,
		"LOGS_TTL":    interval(cfg.LogsTTL),
		"TRACES_TTL":  interval(cfg.TracesTTL),
		"METRICS_TTL": interval(cfg.MetricsTTL),
	}
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	rendered := renderedFS{}
	for _, name := range names {
		content, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		out, err := substitute(content, values)
		if err != nil {
			return nil, fmt.Errorf("render migration %s: %w", name, err)
		}
		rendered[name] = out
	}
	return rendered, nil
}

type renderedFS map[string][]byte

func (r renderedFS) Open(name string) (fs.File, error) {
	if name == "." {
		return renderedDir{}, nil
	}
	content, ok := r[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &renderedFile{Reader: bytes.NewReader(content), info: renderedInfo{name: name, size: len(content)}}, nil
}

func (r renderedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	names := make([]string, 0, len(r))
	for fileName := range r {
		names = append(names, fileName)
	}
	slices.Sort(names)
	entries := make([]fs.DirEntry, 0, len(names))
	for _, fileName := range names {
		info := renderedInfo{name: fileName, size: len(r[fileName])}
		entries = append(entries, fs.FileInfoToDirEntry(info))
	}
	return entries, nil
}

type renderedFile struct {
	*bytes.Reader
	info renderedInfo
}

func (f *renderedFile) Stat() (fs.FileInfo, error) {
	return f.info, nil
}

func (*renderedFile) Close() error {
	return nil
}

type renderedDir struct{}

func (renderedDir) Stat() (fs.FileInfo, error) {
	return renderedInfo{name: ".", dir: true}, nil
}

func (renderedDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: ".", Err: fs.ErrInvalid}
}

func (renderedDir) Close() error {
	return nil
}

type renderedInfo struct {
	name string
	size int
	dir  bool
}

func (i renderedInfo) Name() string {
	return i.name
}

func (i renderedInfo) Size() int64 {
	return int64(i.size)
}

func (i renderedInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir
	}
	return 0
}

func (renderedInfo) ModTime() time.Time {
	return time.Time{}
}

func (i renderedInfo) IsDir() bool {
	return i.dir
}

func (renderedInfo) Sys() any {
	return nil
}
