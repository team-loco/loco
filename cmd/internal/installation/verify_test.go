package installation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerificationUploadsGeneratedSource(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "unavailable"))
	archive, err := verificationArchive("fixture-marker")
	if err != nil {
		t.Fatal(err)
	}
	files, err := archiveFiles(archive)
	if err != nil || string(files["index.html"]) != "fixture-marker" || len(files["proof"]) == 0 {
		t.Fatalf("incomplete source: %v", err)
	}
	if !strings.HasPrefix(string(files["Dockerfile"]), probeDockerfile) {
		t.Fatal("verification source does not use its pinned runtime")
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
	if err := verificationProbe(ctx, server.Client(), server.URL, "fixture-marker"); !errors.Is(err, errVerification) {
		t.Fatalf("canceled probe succeeded: %v", err)
	}
}

func TestVerificationProbeTrustsCAFile(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("fixture-marker")); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caFile, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	system, err := verificationClient("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	probeErr := verificationProbe(ctx, system, server.URL, "fixture-marker")
	if !errors.Is(probeErr, errVerificationHTTPS) {
		t.Fatalf("probe trusted an unknown CA: %v", probeErr)
	}
	trusted, err := verificationClient(caFile)
	if err != nil {
		t.Fatal(err)
	}
	if probeErr := verificationProbe(t.Context(), trusted, server.URL, "fixture-marker"); probeErr != nil {
		t.Fatalf("probe with CA file: %v", probeErr)
	}
}

func TestVerificationRejectsCAFileWithoutCertificate(t *testing.T) {
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verificationClient(caFile); !errors.Is(err, errVerificationCAFile) {
		t.Fatalf("accepted CA file without certificate: %v", err)
	}
}

func archiveFiles(data []byte) (map[string][]byte, error) {
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	result := map[string][]byte{}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(archive)
		if err != nil {
			return nil, err
		}
		result[header.Name] = content
	}
}
