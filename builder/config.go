package main

import (
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/team-loco/loco/builder/internal/extract"
)

const (
	commandFetch       = "fetch"
	commandPush        = "push"
	envSourceURL       = "SOURCE_URL"
	envDownloadTimeout = "DOWNLOAD_TIMEOUT"
)

var (
	errUsage             = errors.New("usage: loco-builder fetch|push [flags]")
	errUnknownCommand    = errors.New("unknown command")
	errNoSourceURL       = errors.New(envSourceURL + " is required")
	errNoDownloadTimeout = errors.New(envDownloadTimeout + " must be a positive duration")
	errNoImageRef        = errors.New("--image-ref is required")
)

type fetchConfig struct {
	Source        extract.Download
	Limits        extract.Limits
	Workspace     string
	Dockerfile    string
	CacheDir      string
	CacheRef      string
	MaxCacheBytes int64
	Insecure      bool
}

type pushConfig struct {
	ImageDir       string
	CacheDir       string
	ImageRef       string
	CacheRef       string
	MaxImageBytes  int64
	MaxCacheBytes  int64
	Insecure       bool
	TerminationLog string
}

type config struct {
	Command string
	Fetch   fetchConfig
	Push    pushConfig
}

func newConfig(args []string, getenv func(string) string) (config, error) {
	if len(args) == 0 {
		return config{}, errUsage
	}
	command := args[0]
	switch command {
	case commandFetch:
		fetch, err := newFetchConfig(args[1:], getenv)
		return config{Command: command, Fetch: fetch}, err
	case commandPush:
		push, err := newPushConfig(args[1:])
		return config{Command: command, Push: push}, err
	default:
		return config{}, fmt.Errorf("%w %q", errUnknownCommand, command)
	}
}

func newFetchConfig(args []string, getenv func(string) string) (fetchConfig, error) {
	cfg := fetchConfig{}
	flags := flag.NewFlagSet(commandFetch, flag.ContinueOnError)
	flags.StringVar(&cfg.Workspace, "workspace", "/workspace", "directory to extract the build context into")
	flags.StringVar(&cfg.Dockerfile, "dockerfile", "Dockerfile", "Dockerfile path relative to the build context")
	flags.Int64Var(&cfg.Source.MaxBytes, "max-source-bytes", 0, "largest source archive to download")
	flags.Int64Var(&cfg.Limits.MaxBytes, "max-context-bytes", 0, "largest extracted build context")
	flags.IntVar(&cfg.Limits.MaxEntries, "max-entries", 0, "most entries in the source archive")
	flags.StringVar(&cfg.CacheDir, "cache-dir", "/cache", "directory to restore the build cache into")
	flags.StringVar(&cfg.CacheRef, "cache-ref", "", "digest reference of the build cache to restore")
	flags.Int64Var(&cfg.MaxCacheBytes, "max-cache-bytes", 0, "largest build cache to restore")
	flags.BoolVar(&cfg.Insecure, "insecure", false, "allow plain HTTP registries")
	if err := flags.Parse(args); err != nil {
		return fetchConfig{}, fmt.Errorf("parse fetch flags: %w", err)
	}

	cfg.Source.URL = getenv(envSourceURL)
	if cfg.Source.URL == "" {
		return fetchConfig{}, errNoSourceURL
	}
	rawTimeout := getenv(envDownloadTimeout)
	timeout, err := time.ParseDuration(rawTimeout)
	if err != nil || timeout <= 0 {
		return fetchConfig{}, fmt.Errorf("%w: %q", errNoDownloadTimeout, rawTimeout)
	}
	cfg.Source.Timeout = timeout
	return cfg, nil
}

func newPushConfig(args []string) (pushConfig, error) {
	cfg := pushConfig{}
	flags := flag.NewFlagSet(commandPush, flag.ContinueOnError)
	flags.StringVar(&cfg.ImageDir, "image-dir", "/out/image", "OCI layout of the built image")
	flags.StringVar(&cfg.CacheDir, "cache-dir", "/out/cache", "OCI layout of the exported build cache")
	flags.StringVar(&cfg.ImageRef, "image-ref", "", "tag to push the image to")
	flags.StringVar(&cfg.CacheRef, "cache-ref", "", "tag to push the build cache to")
	flags.Int64Var(&cfg.MaxImageBytes, "max-image-bytes", 0, "largest image to push")
	flags.Int64Var(&cfg.MaxCacheBytes, "max-cache-bytes", 0, "largest build cache to push")
	flags.BoolVar(&cfg.Insecure, "insecure", false, "allow plain HTTP registries")
	flags.StringVar(&cfg.TerminationLog, "termination-log", "/dev/termination-log", "file to write the result to")
	if err := flags.Parse(args); err != nil {
		return pushConfig{}, fmt.Errorf("parse push flags: %w", err)
	}
	if cfg.ImageRef == "" {
		return pushConfig{}, errNoImageRef
	}
	return cfg, nil
}
