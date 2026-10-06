package loco

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	latestTag     = "v0.0.62"
	releaseAsset  = "loco-linux-amd64"
	releaseBinary = "new loco binary"
)

func newReleaseServer(t *testing.T, checksum string) (*httptest.Server, *int) {
	t.Helper()
	downloads := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+latestTag, http.StatusFound)
	})
	mux.HandleFunc("/releases/download/"+latestTag+"/"+releaseAsset, func(w http.ResponseWriter, _ *http.Request) {
		downloads++
		fmt.Fprint(w, releaseBinary)
	})
	mux.HandleFunc("/releases/download/"+latestTag+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  loco-darwin-arm64\n%s  %s\n", strings.Repeat("0", 64), checksum, releaseAsset)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &downloads
}

func binaryChecksum() string {
	sum := sha256.Sum256([]byte(releaseBinary))
	return hex.EncodeToString(sum[:])
}

func newTestUpdater(srv *httptest.Server) updater {
	return updater{
		client:      srv.Client(),
		releasesURL: srv.URL + "/releases",
		goos:        "linux",
		goarch:      "amd64",
	}
}

func writeOldBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "loco")
	if err := os.WriteFile(path, []byte("old loco binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestUpdateReplacesBinary(t *testing.T) {
	srv, _ := newReleaseServer(t, binaryChecksum())
	path := writeOldBinary(t)
	var out bytes.Buffer

	if err := newTestUpdater(srv).run(context.Background(), &out, "v0.0.61", path); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != releaseBinary {
		t.Fatalf("binary = %q, want %q", got, releaseBinary)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}
	if !strings.Contains(out.String(), "Updated loco v0.0.61 -> "+latestTag) {
		t.Fatalf("output = %q", out.String())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only the binary to remain, found %d entries", len(entries))
	}
}

func TestUpdateFollowsSymlink(t *testing.T) {
	srv, _ := newReleaseServer(t, binaryChecksum())
	target := writeOldBinary(t)
	link := filepath.Join(t.TempDir(), "loco")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := newTestUpdater(srv).run(context.Background(), &bytes.Buffer{}, "v0.0.61", link); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, target); got != releaseBinary {
		t.Fatalf("target = %q, want %q", got, releaseBinary)
	}
	if dest, err := os.Readlink(link); err != nil || dest != target {
		t.Fatalf("symlink = %q, %v; want it to still point at %q", dest, err, target)
	}
}

func TestUpdateAlreadyLatest(t *testing.T) {
	srv, downloads := newReleaseServer(t, binaryChecksum())
	path := writeOldBinary(t)
	var out bytes.Buffer

	if err := newTestUpdater(srv).run(context.Background(), &out, latestTag, path); err != nil {
		t.Fatal(err)
	}
	if *downloads != 0 {
		t.Fatalf("downloaded the binary %d times, want 0", *downloads)
	}
	if !strings.Contains(out.String(), "already the latest version") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestUpdateRejectsChecksumMismatch(t *testing.T) {
	srv, _ := newReleaseServer(t, strings.Repeat("f", 64))
	path := writeOldBinary(t)

	err := newTestUpdater(srv).run(context.Background(), &bytes.Buffer{}, "v0.0.61", path)
	if err == nil || !strings.Contains(err.Error(), "checksum verification failed") {
		t.Fatalf("err = %v, want checksum failure", err)
	}
	if got := readFile(t, path); got != "old loco binary" {
		t.Fatalf("binary was replaced despite the checksum mismatch: %q", got)
	}
}

func TestUpdateRejectsUnsupportedPlatform(t *testing.T) {
	srv, _ := newReleaseServer(t, binaryChecksum())
	u := newTestUpdater(srv)
	u.goos = "windows"

	err := u.run(context.Background(), &bytes.Buffer{}, "v0.0.61", writeOldBinary(t))
	if err == nil || !strings.Contains(err.Error(), "no loco release for windows") {
		t.Fatalf("err = %v, want unsupported platform", err)
	}
}
