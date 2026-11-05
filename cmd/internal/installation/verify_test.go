package installation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerificationUploadsGeneratedSource(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "unavailable"))
	archive, err := verificationArchive("busybox@sha256:fixture", "fixture-marker")
	if err != nil {
		t.Fatal(err)
	}
	files, err := archiveFiles(archive)
	if err != nil || string(files["index.html"]) != "fixture-marker" || len(files["proof"]) == 0 {
		t.Fatalf("incomplete source: %v", err)
	}
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(compressed)
	var count int
	for {
		header, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil || header.Typeflag != tar.TypeReg || header.Mode != 0600 {
			t.Fatalf("source file metadata: %v %v", header, readErr)
		}
		count++
	}
	if closeErr := compressed.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if count != len(files) || count != 3 {
		t.Fatalf("source contains %d files", count)
	}
	uploaded := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil || !bytes.Equal(body, archive) || r.Method != http.MethodPut ||
			r.Header.Get("Content-Type") != "application/gzip" {
			t.Error("source upload changed")
		}
		uploaded = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	if err := verificationUpload(t.Context(), server.URL, archive); err != nil || !uploaded {
		t.Fatalf("upload: %v", err)
	}
}

func TestVerificationProbeHonorsCancellation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("fixture-marker")); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := verificationProbe(ctx, server.URL, "fixture-marker"); !errors.Is(err, errVerification) {
		t.Fatalf("canceled probe succeeded: %v", err)
	}
}

func TestVerificationRequiresPinnedRuntimeFromReleaseChart(t *testing.T) {
	for _, image := range []string{"busybox:latest", "busybox:1.0\nRUN echo injected"} {
		chart := verificationChart(t, "otel-col-daemon:\n  initContainers:\n    - image: "+image+"\n")
		if _, err := verificationBase(chart); err == nil {
			t.Fatal("accepted unpinned verification runtime")
		}
	}
	image := "busybox:fixture@sha256:" + strings.Repeat("a", 64)
	chart := verificationChart(t, "otel-col-daemon:\n  initContainers:\n    - image: "+image+"\n")
	if got, err := verificationBase(chart); err != nil || got != image {
		t.Fatalf("runtime %q: %v", got, err)
	}
}

func verificationChart(t *testing.T, values string) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(
		&tar.Header{Name: "loco-worker/values.yaml", Size: int64(len(values)), Mode: 0600},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(values)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
