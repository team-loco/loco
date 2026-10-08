package extract

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	etcPasswd       = "/etc/passwd"
	wantAbsolute    = "absolute"
	wantUnsupported = "unsupported type"
	linkName        = "link"

	testDownloadTimeout  = time.Minute
	shortDownloadTimeout = 50 * time.Millisecond
)

type entry struct {
	name     string
	typeflag byte
	body     string
	linkname string
	mode     int64
}

func archive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Linkname: e.linkname,
			Mode:     mode,
			Size:     int64(len(e.body)),
		}
		if e.typeflag != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", e.name, err)
		}
		if hdr.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("write body %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func extractInto(t *testing.T, data []byte, limits Limits) (string, error) {
	t.Helper()
	parent := t.TempDir()
	dest := filepath.Join(parent, "workspace")
	if err := os.Mkdir(dest, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	reader := bytes.NewReader(data)
	return dest, Archive(reader, dest, limits)
}

func TestArchiveExtractsRegularContext(t *testing.T) {
	data := archive(t,
		entry{name: "./", typeflag: tar.TypeDir, mode: 0o755},
		entry{name: "Dockerfile", typeflag: tar.TypeReg, body: "FROM scratch\n"},
		entry{name: "bin/", typeflag: tar.TypeDir, mode: 0o755},
		entry{name: "bin/run.sh", typeflag: tar.TypeReg, body: "#!/bin/sh\n", mode: 0o4755},
		entry{name: "node_modules/pkg/cli.js", typeflag: tar.TypeReg, body: "x"},
		entry{name: "node_modules/.bin/cli", typeflag: tar.TypeSymlink, linkname: "../pkg/cli.js"},
		entry{name: "copy.sh", typeflag: tar.TypeLink, linkname: "bin/run.sh"},
		entry{name: "dangling", typeflag: tar.TypeSymlink, linkname: "missing/file"},
	)
	dest, err := extractInto(t, data, Limits{MaxBytes: 1 << 20, MaxEntries: 100})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "node_modules", ".bin", "cli"))
	if err != nil || string(got) != "x" {
		t.Fatalf("symlink content = %q, %v", got, err)
	}
	info, err := os.Stat(filepath.Join(dest, "bin", "run.sh"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o755 || info.Mode()&os.ModeSetuid != 0 {
		t.Errorf("run.sh mode = %v, want 0755 without setuid", info.Mode())
	}
	if err := CheckDockerfile(dest, "Dockerfile"); err != nil {
		t.Errorf("CheckDockerfile: %v", err)
	}
}

func TestArchiveRejectsMaliciousEntries(t *testing.T) {
	cases := []struct {
		name    string
		entries []entry
		want    string
	}{
		{
			name:    "absolute path",
			entries: []entry{{name: etcPasswd, typeflag: tar.TypeReg, body: "x"}},
			want:    wantAbsolute,
		},
		{
			name:    "parent traversal",
			entries: []entry{{name: "a/../../escape", typeflag: tar.TypeReg, body: "x"}},
			want:    "leaves the build context",
		},
		{
			name:    "absolute symlink",
			entries: []entry{{name: linkName, typeflag: tar.TypeSymlink, linkname: etcPasswd}},
			want:    wantAbsolute,
		},
		{
			name:    "symlink escaping lexically",
			entries: []entry{{name: "dir/link", typeflag: tar.TypeSymlink, linkname: "../../outside"}},
			want:    "outside the build context",
		},
		{
			name: "symlink escaping through another symlink",
			entries: []entry{
				{name: "here", typeflag: tar.TypeSymlink, linkname: "."},
				{name: "up", typeflag: tar.TypeSymlink, linkname: "here/.."},
				{name: "nested/q", typeflag: tar.TypeSymlink, linkname: "../here/.."},
			},
			want: "outside the build context",
		},
		{
			name: "write through escaping symlink",
			entries: []entry{
				{name: "here", typeflag: tar.TypeSymlink, linkname: "."},
				{name: "up", typeflag: tar.TypeSymlink, linkname: "here/.."},
				{name: "up/escaped", typeflag: tar.TypeReg, body: "x"},
			},
			want: "escapes",
		},
		{
			name:    "hard link outside",
			entries: []entry{{name: linkName, typeflag: tar.TypeLink, linkname: "../outside"}},
			want:    "leaves the build context",
		},
		{
			name:    "hard link to absolute",
			entries: []entry{{name: linkName, typeflag: tar.TypeLink, linkname: etcPasswd}},
			want:    wantAbsolute,
		},
		{
			name: "hard link to symlink",
			entries: []entry{
				{name: "dir/l", typeflag: tar.TypeSymlink, linkname: "../x"},
				{name: "l2", typeflag: tar.TypeLink, linkname: "dir/l"},
			},
			want: "not a regular file",
		},
		{
			name:    "character device",
			entries: []entry{{name: "null", typeflag: tar.TypeChar}},
			want:    wantUnsupported,
		},
		{
			name:    "block device",
			entries: []entry{{name: "sda", typeflag: tar.TypeBlock}},
			want:    wantUnsupported,
		},
		{
			name:    "fifo",
			entries: []entry{{name: "pipe", typeflag: tar.TypeFifo}},
			want:    wantUnsupported,
		},
		{
			name: "file replacing a directory",
			entries: []entry{
				{name: "dir/file", typeflag: tar.TypeReg, body: "x"},
				{name: "dir", typeflag: tar.TypeReg, body: "y"},
			},
			want: "replaces a directory",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := archive(t, tc.entries...)
			dest, err := extractInto(t, data, Limits{MaxBytes: 1 << 20, MaxEntries: 100})
			if err == nil {
				t.Fatalf("Archive succeeded, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
			parent := filepath.Dir(dest)
			for _, leaked := range []string{"escape", "outside", "escaped"} {
				if _, statErr := os.Lstat(filepath.Join(parent, leaked)); statErr == nil {
					t.Errorf("%s was written outside the workspace", leaked)
				}
			}
		})
	}
}

func TestArchiveEnforcesLimits(t *testing.T) {
	big := strings.Repeat("a", 2048)
	data := archive(t, entry{name: "big", typeflag: tar.TypeReg, body: big})
	_, err := extractInto(t, data, Limits{MaxBytes: 1024})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("size limit error = %v, want ErrLimitExceeded", err)
	}

	many := archive(t,
		entry{name: "a", typeflag: tar.TypeReg, body: "1"},
		entry{name: "b", typeflag: tar.TypeReg, body: "2"},
		entry{name: "c", typeflag: tar.TypeReg, body: "3"},
	)
	_, err = extractInto(t, many, Limits{MaxEntries: 2})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("entry limit error = %v, want ErrLimitExceeded", err)
	}
}

func TestArchiveRejectsGarbage(t *testing.T) {
	_, err := extractInto(t, []byte("not gzip"), Limits{})
	if err == nil {
		t.Fatal("Archive accepted a non-gzip stream")
	}
}

func TestCheckDockerfile(t *testing.T) {
	data := archive(t,
		entry{name: "real/Dockerfile", typeflag: tar.TypeReg, body: "FROM scratch\n"},
		entry{name: "linked", typeflag: tar.TypeSymlink, linkname: "real/Dockerfile"},
	)
	dest, err := extractInto(t, data, Limits{})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := CheckDockerfile(dest, "real/Dockerfile"); err != nil {
		t.Errorf("nested Dockerfile: %v", err)
	}
	if err := CheckDockerfile(dest, "Dockerfile"); err == nil {
		t.Error("missing Dockerfile accepted")
	}
	if err := CheckDockerfile(dest, "linked"); err == nil {
		t.Error("symlinked Dockerfile accepted")
	}
	if err := CheckDockerfile(dest, "../Dockerfile"); err == nil {
		t.Error("Dockerfile outside the context accepted")
	}
}

func testDownload(url string, maxBytes int64) Download {
	return Download{URL: url, Timeout: testDownloadTimeout, MaxBytes: maxBytes}
}

func TestFetchStopsAtTimeout(t *testing.T) {
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	source := Download{URL: server.URL, Timeout: shortDownloadTimeout}
	err := Fetch(t.Context(), source, t.TempDir(), Limits{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Fetch past the timeout = %v, want context.DeadlineExceeded", err)
	}
}

func TestFetchCapsDownload(t *testing.T) {
	data := archive(t, entry{name: "Dockerfile", typeflag: tar.TypeReg, body: strings.Repeat("x", 4096)})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		if _, err := w.Write(data); err != nil {
			t.Errorf("write response: %v", err)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	dest := t.TempDir()
	err := Fetch(t.Context(), testDownload(server.URL, int64(len(data)-1)), dest, Limits{})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("Fetch over the cap = %v, want ErrLimitExceeded", err)
	}

	dest = t.TempDir()
	if err := Fetch(t.Context(), testDownload(server.URL, int64(len(data))), dest, Limits{}); err != nil {
		t.Errorf("Fetch at the cap: %v", err)
	}
}

func TestFetchRejectsErrorStatus(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	err := Fetch(t.Context(), testDownload(server.URL, 0), t.TempDir(), Limits{})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("Fetch = %v, want a 403 error", err)
	}
}
