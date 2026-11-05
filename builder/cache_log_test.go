package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

const (
	levelError = "level=ERROR"
	levelWarn  = "level=WARN"
)

func TestCacheFailureLevel(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"credential rejected": {
			err:  fmt.Errorf("fetch cache: %w", &transport.Error{StatusCode: http.StatusUnauthorized}),
			want: levelError,
		},
		"forbidden": {
			err:  fmt.Errorf("push cache: %w", &transport.Error{StatusCode: http.StatusForbidden}),
			want: levelError,
		},
		"cache missing": {
			err:  fmt.Errorf("fetch cache: %w", &transport.Error{StatusCode: http.StatusNotFound}),
			want: levelWarn,
		},
		"registry unavailable": {
			err:  fmt.Errorf("fetch cache: %w", &transport.Error{StatusCode: http.StatusServiceUnavailable}),
			want: levelWarn,
		},
		"network error": {err: errors.New("connection reset"), want: levelWarn},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			logCacheFailure(context.Background(), "cache unavailable", tc.err)
			if !strings.Contains(logs.String(), tc.want) {
				t.Errorf("log %q does not contain %s", logs.String(), tc.want)
			}
		})
	}
}
