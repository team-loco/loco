package migrations

import (
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"testing/fstest"
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
		"DATABASE":         cfg.Database,
		"LOGS_TTL_SECONDS": strconv.FormatInt(int64(cfg.LogsTTL/time.Second), 10),
		"TRACES_TTL":       interval(cfg.TracesTTL),
		"METRICS_TTL":      interval(cfg.MetricsTTL),
	}
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
