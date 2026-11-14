package migrations

import (
	"fmt"
	"io/fs"
	"regexp"
	"testing/fstest"
)

var placeholderPattern = regexp.MustCompile(`\$\{([A-Z_]+)\}`)

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
	values := map[string]string{"DATABASE": cfg.Database}
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	rendered := fstest.MapFS{}
	for _, name := range names {
		content, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		out, err := substitute(content, values)
		if err != nil {
			return nil, fmt.Errorf("render migration %s: %w", name, err)
		}
		rendered[name] = &fstest.MapFile{Data: out}
	}
	return rendered, nil
}
