package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"

	"github.com/team-loco/loco/tools/internal/imagepull"
)

var errUsage = errors.New("usage: pullimage IMAGE [IMAGE]")

func imagePresent(ctx context.Context, image string) bool {
	return exec.CommandContext(ctx, "docker", "image", "inspect", "--", image).Run() == nil //nolint:gosec
}

func run(ctx context.Context, images []string) error {
	if len(images) == 0 {
		return errUsage
	}
	for _, image := range images {
		if imagePresent(ctx, image) {
			continue
		}
		if err := imagepull.Pull(ctx, image); err != nil {
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
