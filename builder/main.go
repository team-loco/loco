package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/team-loco/loco/builder/internal/extract"
	"github.com/team-loco/loco/builder/internal/ocilayout"
	"github.com/team-loco/loco/builder/internal/registry"
)

const terminationLogPerm = 0o600

type result struct {
	ImageDigest string `json:"imageDigest"`
	CacheDigest string `json:"cacheDigest,omitempty"`
}

func main() {
	handler := slog.NewTextHandler(os.Stderr, nil)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	cfg, err := newConfig(os.Args[1:], os.Getenv)
	if err != nil {
		panic(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, cfg)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config) error {
	if cfg.Command == commandPush {
		return push(ctx, cfg.Push)
	}
	return fetch(ctx, cfg.Fetch)
}

func fetch(ctx context.Context, cfg fetchConfig) error {
	if err := extract.Fetch(ctx, cfg.Source, cfg.Workspace, cfg.Limits); err != nil {
		return fmt.Errorf("fetch source: %w", err)
	}
	if err := extract.CheckDockerfile(cfg.Workspace, cfg.Dockerfile); err != nil {
		return err
	}
	slog.InfoContext(ctx, "source extracted", "workspace", cfg.Workspace)

	if cfg.CacheRef == "" {
		return nil
	}
	client := registry.New(ctx, cfg.Insecure)
	if err := client.RestoreCache(cfg.CacheRef, cfg.CacheDir, cfg.MaxCacheBytes); err != nil {
		slog.WarnContext(ctx, "building without cache", "error", err)
		return nil
	}
	slog.InfoContext(ctx, "cache restored", "ref", cfg.CacheRef)
	return nil
}

func push(ctx context.Context, cfg pushConfig) error {
	imageBytes, err := ocilayout.Validate(cfg.ImageDir, cfg.MaxImageBytes)
	if err != nil {
		return fmt.Errorf("validate image: %w", err)
	}
	slog.InfoContext(ctx, "image validated", "bytes", imageBytes)

	client := registry.New(ctx, cfg.Insecure)
	imageDigest, err := client.PushLayout(cfg.ImageDir, cfg.ImageRef, false)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "image pushed", "ref", cfg.ImageRef, "digest", imageDigest.String())
	out := result{ImageDigest: imageDigest.String()}

	if cfg.CacheRef != "" {
		cacheDigest, cacheErr := pushCache(ctx, client, cfg.CacheDir, cfg.CacheRef, cfg.MaxCacheBytes)
		if cacheErr != nil {
			slog.WarnContext(ctx, "build cache not saved", "error", cacheErr)
		}
		out.CacheDigest = cacheDigest
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	if err := os.WriteFile(cfg.TerminationLog, encoded, terminationLogPerm); err != nil {
		return fmt.Errorf("write %s: %w", cfg.TerminationLog, err)
	}
	return nil
}

func pushCache(ctx context.Context, client *registry.Client, dir, ref string, maxBytes int64) (string, error) {
	cacheBytes, err := ocilayout.Validate(dir, maxBytes)
	if err != nil {
		return "", fmt.Errorf("validate cache: %w", err)
	}
	digest, err := client.PushLayout(dir, ref, true)
	if err != nil {
		return "", err
	}
	slog.InfoContext(ctx, "cache pushed", "ref", ref, "digest", digest.String(), "bytes", cacheBytes)
	return digest.String(), nil
}
