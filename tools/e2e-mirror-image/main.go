package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

var errUsage = errors.New("usage: e2e-mirror-image SOURCE DESTINATION [CMD...]")

const unknownPlatform = "unknown"

func run(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return errUsage
	}
	if len(args) == 2 {
		if err := crane.Copy(args[0], args[1], crane.WithContext(ctx)); err != nil {
			return fmt.Errorf("mirror public fixture image: %w", err)
		}
		return nil
	}
	if err := mirrorWithCmd(ctx, args[0], args[1], args[2:]); err != nil {
		return fmt.Errorf("mirror public fixture image with a new command: %w", err)
	}
	return nil
}

// mirrorWithCmd copies every platform of an image index with its command replaced, so a
// fixture can behave differently without a second pinned base image.
func mirrorWithCmd(ctx context.Context, source, destination string, cmd []string) error {
	src, err := name.ParseReference(source)
	if err != nil {
		return err
	}
	dst, err := name.ParseReference(destination)
	if err != nil {
		return err
	}
	index, err := remote.Index(src, remote.WithContext(ctx))
	if err != nil {
		return err
	}
	manifest, err := index.IndexManifest()
	if err != nil {
		return err
	}
	var mutated v1.ImageIndex = empty.Index
	for _, descriptor := range manifest.Manifests {
		// Attestation manifests carry no runnable image config.
		if descriptor.Platform == nil || descriptor.Platform.OS == unknownPlatform {
			continue
		}
		image, err := index.Image(descriptor.Digest)
		if err != nil {
			return err
		}
		config, err := image.ConfigFile()
		if err != nil {
			return err
		}
		config = config.DeepCopy()
		config.Config.Cmd = cmd
		image, err = mutate.ConfigFile(image, config)
		if err != nil {
			return err
		}
		mutated = mutate.AppendManifests(mutated, mutate.IndexAddendum{Add: image, Platform: descriptor.Platform})
	}
	return remote.WriteIndex(dst, mutated, remote.WithContext(ctx))
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
