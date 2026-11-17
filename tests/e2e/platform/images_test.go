package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPullPrebuiltPublishedManifest(t *testing.T) {
	const reference = "registry.test/loco-api:published"
	const missingManifest = "missing"
	digest := "registry.test/loco-api@sha256:" + strings.Repeat("a", 64)
	index := `{"manifests":[{"digest":"sha256:` + strings.Repeat("b", 64) +
		`","platform":{"os":"linux","architecture":"arm64"}},{"digest":"sha256:` +
		strings.Repeat("a", 64) + `","platform":{"os":"linux","architecture":"amd64"}}]}`
	for _, tc := range []struct {
		name      string
		manifest  string
		failure   string
		failures  string
		wantCalls int
		stop      error
	}{
		{"published child becomes readable", index, missingManifest, "1", 2, nil},
		{"authentication fails immediately", index, "unauthorized", "1", 1, nil},
		{"unindexed missing image fails immediately", `{}`, missingManifest, "1", 1, nil},
		{"missing blob fails immediately", index, "blob", "1", 1, nil},
		{"published child stays missing", index, missingManifest, "10000", 0, context.DeadlineExceeded},
		{"caller cancels a missing child", index, missingManifest, "10000", 0, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$FAKE_CALLS"
case "$1" in
version) printf amd64 ;;
buildx) printf '%s' "$FAKE_MANIFEST" ;;
pull)
  calls=$(grep -c '^pull ' "$FAKE_CALLS")
  if [ "$calls" -le "$FAKE_FAILURES" ]; then
    case "$FAKE_FAILURE" in
    missing) printf 'Error response from daemon: failed to resolve reference "%s": %s: not found\n' "$3" "$3" >&2 ;;
    unauthorized) printf 'unauthorized: authentication required\n' >&2 ;;
    blob) printf 'failed to copy: blob sha256:missing: not found\n' >&2 ;;
    esac
    exit 1
  fi ;;
tag) printf '%s\n' "$2 $3" > "$FAKE_TAG" ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			callsPath := filepath.Join(dir, "calls")
			tagPath := filepath.Join(dir, "tag")
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("FAKE_CALLS", callsPath)
			t.Setenv("FAKE_TAG", tagPath)
			t.Setenv("FAKE_MANIFEST", tc.manifest)
			t.Setenv("FAKE_FAILURE", tc.failure)
			t.Setenv("FAKE_FAILURES", tc.failures)
			cluster := &Cluster{root: dir, env: os.Environ()}
			ctx := t.Context()
			if tc.stop != nil {
				var cancel context.CancelFunc
				if errors.Is(tc.stop, context.Canceled) {
					ctx, cancel = context.WithCancel(ctx)
					timer := time.AfterFunc(time.Second, cancel)
					defer timer.Stop()
				} else {
					ctx, cancel = context.WithTimeout(ctx, time.Second)
				}
				defer cancel()
			}
			err := cluster.pullPrebuilt(ctx, reference, "local:test", 0)
			calls, readErr := os.ReadFile(callsPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.stop != nil {
				if !errors.Is(err, tc.stop) || !strings.Contains(err.Error(), digest+": not found") {
					t.Fatalf("error %v, want %v and original missing manifest", err, tc.stop)
				}
			} else if got := strings.Count(string(calls), "pull "); got != tc.wantCalls {
				t.Fatalf("pull calls = %d, want %d; error %v; commands:\n%s", got, tc.wantCalls, err, calls)
			}
			if tc.name == "published child becomes readable" {
				if err != nil {
					t.Fatal(err)
				}
				tag, tagErr := os.ReadFile(tagPath)
				if tagErr != nil || strings.TrimSpace(string(tag)) != digest+" local:test" {
					t.Fatalf("tag = %q, error %v, want selected immutable amd64 digest", tag, tagErr)
				}
				return
			}
			if err == nil || strings.Contains(string(calls), "tag ") {
				t.Fatalf("error = %v; failed pull must not be tagged; commands:\n%s", err, calls)
			}
		})
	}
}
