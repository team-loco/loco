package main

import (
	"errors"
	"testing"
	"time"
)

const (
	testSourceURL    = "https://bucket.example.com/sources/b-1.tar.gz?X-Amz-Signature=abc"
	testImageRef     = "registry.example.com/loco/ws-1/res-1:build-b-1"
	testCacheRef     = "registry.example.com/loco/ws-1/res-1@sha256:abc"
	testFetchTimeout = 7 * time.Minute
)

func testEnv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func fetchEnv() map[string]string {
	return map[string]string{envSourceURL: testSourceURL, envDownloadTimeout: testFetchTimeout.String()}
}

func TestNewConfigFetch(t *testing.T) {
	args := []string{
		commandFetch,
		"--workspace=/work",
		"--dockerfile=deploy/Dockerfile",
		"--max-source-bytes=100",
		"--max-context-bytes=200",
		"--max-entries=3",
		"--cache-ref=" + testCacheRef,
		"--max-cache-bytes=400",
		"--insecure",
	}
	cfg, err := newConfig(args, testEnv(fetchEnv()))
	if err != nil {
		t.Fatalf("newConfig: %v", err)
	}
	fetch := cfg.Fetch
	if cfg.Command != commandFetch || fetch.Source.URL != testSourceURL || fetch.Source.Timeout != testFetchTimeout {
		t.Errorf("command %q, source %+v", cfg.Command, fetch.Source)
	}
	if fetch.Source.MaxBytes != 100 || fetch.Limits.MaxBytes != 200 || fetch.Limits.MaxEntries != 3 {
		t.Errorf("source %+v, limits %+v", fetch.Source, fetch.Limits)
	}
	if fetch.Workspace != "/work" || fetch.Dockerfile != "deploy/Dockerfile" || fetch.CacheDir != "/cache" {
		t.Errorf("paths = %+v", fetch)
	}
	if fetch.CacheRef != testCacheRef || fetch.MaxCacheBytes != 400 || !fetch.Insecure {
		t.Errorf("cache = %+v", fetch)
	}
}

func TestNewConfigPush(t *testing.T) {
	args := []string{commandPush, "--image-ref=" + testImageRef, "--max-image-bytes=500"}
	cfg, err := newConfig(args, testEnv(nil))
	if err != nil {
		t.Fatalf("newConfig: %v", err)
	}
	push := cfg.Push
	if cfg.Command != commandPush || push.ImageRef != testImageRef || push.MaxImageBytes != 500 {
		t.Errorf("command %q, push %+v", cfg.Command, push)
	}
	if push.ImageDir != "/out/image" || push.TerminationLog != "/dev/termination-log" || push.Insecure {
		t.Errorf("defaults = %+v", push)
	}
}

func TestNewConfigRejectsInvalid(t *testing.T) {
	noURL := fetchEnv()
	delete(noURL, envSourceURL)
	noTimeout := fetchEnv()
	delete(noTimeout, envDownloadTimeout)
	badTimeout := fetchEnv()
	badTimeout[envDownloadTimeout] = "ten minutes"
	zeroTimeout := fetchEnv()
	zeroTimeout[envDownloadTimeout] = "0s"

	cases := map[string]struct {
		args []string
		env  map[string]string
		want error
	}{
		"no command":       {args: nil, env: fetchEnv(), want: errUsage},
		"unknown command":  {args: []string{"build"}, env: fetchEnv(), want: errUnknownCommand},
		"no source url":    {args: []string{commandFetch}, env: noURL, want: errNoSourceURL},
		"no timeout":       {args: []string{commandFetch}, env: noTimeout, want: errNoDownloadTimeout},
		"unparsed timeout": {args: []string{commandFetch}, env: badTimeout, want: errNoDownloadTimeout},
		"zero timeout":     {args: []string{commandFetch}, env: zeroTimeout, want: errNoDownloadTimeout},
		"no image ref":     {args: []string{commandPush}, env: nil, want: errNoImageRef},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := newConfig(tc.args, testEnv(tc.env))
			if !errors.Is(err, tc.want) {
				t.Errorf("newConfig = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNewConfigRejectsUnparsableFlag(t *testing.T) {
	args := []string{commandFetch, "--max-entries=many"}
	if _, err := newConfig(args, testEnv(fetchEnv())); err == nil {
		t.Error("newConfig accepted an unparsable flag")
	}
}
