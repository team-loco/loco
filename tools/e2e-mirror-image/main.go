package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/google/go-containerregistry/pkg/crane"
)

var errUsage = errors.New("usage: e2e-mirror-image SOURCE DESTINATION")

func run(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return errUsage
	}
	if err := crane.Copy(args[0], args[1], crane.WithContext(ctx)); err != nil {
		return fmt.Errorf("mirror public fixture image: %w", err)
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		slog.ErrorContext(ctx, "mirror image", "error", err)
		os.Exit(1)
	}
}
