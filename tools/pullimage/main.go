package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"
)

const (
	pullAttempts   = 5
	pullRetryDelay = 10 * time.Second
)

var (
	errUsage = errors.New("usage: pullimage IMAGE [IMAGE]")

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

type pullFunc func(ctx context.Context, image string) ([]byte, error)

func dockerPull(ctx context.Context, image string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "pull", "-q", "--", image).CombinedOutput() //nolint:gosec
}

func imagePresent(ctx context.Context, image string) bool {
	return exec.CommandContext(ctx, "docker", "image", "inspect", "--", image).Run() == nil //nolint:gosec
}

func transient(output []byte) bool {
	text := string(output)
	for _, failure := range transientFailures {
		if strings.Contains(text, failure) {
			return true
		}
	}
	return false
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

func run(ctx context.Context, images []string) error {
	if len(images) == 0 {
		return errUsage
	}
	for _, image := range images {
		if imagePresent(ctx, image) {
			continue
		}
		if err := pullWithRetry(ctx, dockerPull, image, pullAttempts, pullRetryDelay); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		slog.ErrorContext(ctx, "pull images", "error", err)
		os.Exit(1)
	}
}
