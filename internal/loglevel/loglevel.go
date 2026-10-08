package loglevel

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

var ErrUnknown = errors.New("is not a log level: use debug, info, warn or error")

var levels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

func Parse(raw string) (slog.Level, error) {
	name := strings.ToLower(raw)
	level, ok := levels[name]
	if !ok {
		return slog.LevelInfo, fmt.Errorf("%q %w", raw, ErrUnknown)
	}
	return level, nil
}
