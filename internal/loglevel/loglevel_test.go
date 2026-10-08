package loglevel

import (
	"errors"
	"log/slog"
	"testing"
)

func TestParse(t *testing.T) {
	valid := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"DEBUG": slog.LevelDebug,
		"Warn":  slog.LevelWarn,
	}
	for raw, want := range valid {
		got, err := Parse(raw)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v, want %v", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "-4", "0", " warn", "warning", "WARN+2", "trace"} {
		if _, err := Parse(raw); !errors.Is(err, ErrUnknown) {
			t.Errorf("Parse(%q) error = %v, want ErrUnknown", raw, err)
		}
	}
}
