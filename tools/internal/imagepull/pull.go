package imagepull

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	pullAttempts   = 5
	pullRetryDelay = 10 * time.Second
)

var (
	registryHTTP5xx = regexp.MustCompile(`unexpected status from [^\n]+: 5[0-9]{2}\b`)

	transientFailures = []string{
		"500 Internal Server Error",
		"502 Bad Gateway",
		"503 Service Unavailable",
		"504 Gateway Timeout",
		"i/o timeout",
		"TLS handshake timeout",
		"connection reset by peer",
		"unexpected EOF",
	}
)

// Pull pulls an image with bounded retries for transient registry failures.
func Pull(ctx context.Context, image string) error {
	return pullWithRetry(ctx, dockerPull, image, pullAttempts, pullRetryDelay)
}

type pullFunc func(ctx context.Context, image string) ([]byte, error)

func dockerPull(ctx context.Context, image string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "pull", "-q", "--", image).CombinedOutput()
}

func transient(output []byte) bool {
	if registryHTTP5xx.Match(output) {
		return true
	}
	text := string(output)
	for _, failure := range transientFailures {
		if strings.Contains(text, failure) {
			return true
		}
	}
	return strings.HasSuffix(strings.TrimSpace(text), ": EOF")
}

func pullWithRetry(ctx context.Context, pull pullFunc, image string, attempts int, delay time.Duration) error {
	for attempt := 1; ; attempt++ {
		output, err := pull(ctx, image)
		if err == nil {
			return nil
		}
		output = bytes.TrimSpace(output)
		if attempt == attempts || !transient(output) {
			return fmt.Errorf("pull %s after %d attempts: %w: %s", image, attempt, err, output)
		}
		slog.WarnContext(ctx, "retrying image pull", "image", image, "attempt", attempt, "output", string(output))
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("pull %s: %w", image, ctx.Err())
		}
	}
}
