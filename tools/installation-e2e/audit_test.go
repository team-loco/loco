package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testCanary = "installation-canary-8f48cbb251b7450e"

func TestAuditCommandOutput(t *testing.T) {
	for _, status := range []string{"0", "9"} {
		t.Run(status, func(t *testing.T) {
			dir := t.TempDir()
			// Fixed fixture command exercises the real command helper on both exit paths.
			executable := filepath.Join(dir, "locoadm")
			script := "#!/bin/sh\nprintf '%s' 'stdout-" + testCanary + "'\n" +
				"printf '%s' 'stderr-" + testCanary + "' >&2\nexit " + status + "\n"
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			audit := newLeakAudit()
			ctx := context.WithValue(context.Background(), auditKey{}, audit)
			_, commandErr := command(ctx, "", nil, "locoadm")
			if (commandErr == nil) != (status == "0") {
				t.Fatal("unexpected command exit")
			}
			if commandErr != nil && strings.Contains(commandErr.Error(), testCanary) {
				t.Fatal("command error exposes canary")
			}
			// Canary discovery after a command must also find its earlier output.
			if err := audit.check("before discovery"); err != nil {
				t.Fatal(err)
			}
			audit.add([]byte("stdout-" + testCanary))
			assertAuditLeak(t, audit, "CLI output (stdout)")
			audit.canaries = nil
			audit.add([]byte("stderr-" + testCanary))
			assertAuditLeak(t, audit, "CLI output (stderr)")
		})
	}
}
func TestAuditSurfaces(t *testing.T) {
	for _, category := range []string{"subprocess arguments", "operator files", "CLI output"} {
		t.Run(category, func(t *testing.T) {
			audit := newLeakAudit()
			audit.add([]byte(testCanary))
			audit.capture(category, []byte("safe metadata: cluster-id public-fingerprint CA certificate"))
			if err := audit.check("benign"); err != nil {
				t.Fatal(err)
			}
			audit.capture(category, []byte(base64.StdEncoding.EncodeToString([]byte(testCanary))))
			assertAuditLeak(t, audit, category)
		})
	}
}
func TestAuditHelmHistory(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(`{"config":{"password":"` + testCanary + `"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(compressed.Bytes()))
	audit := newLeakAudit()
	audit.add([]byte(testCanary))
	// Encoded storage hides plaintext; the acceptance path must decode Helm's payload.
	audit.capture("Helm history", encoded)
	if err := audit.check("encoded"); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeRelease(encoded)
	if err != nil {
		t.Fatal(err)
	}
	audit.capture("Helm history", decoded)
	assertAuditLeak(t, audit, "Helm history")
}
func TestAuditBounds(t *testing.T) {
	audit := newLeakAudit()
	audit.capture("CLI output", make([]byte, auditLimit+1))
	if err := audit.check("bounded"); err == nil {
		t.Fatal("unbounded audit accepted")
	}
}
func assertAuditLeak(t *testing.T, audit *leakAudit, category string) {
	t.Helper()
	err := audit.check("test phase")
	if err == nil {
		t.Fatal("canary leak was accepted")
	}
	if strings.Contains(err.Error(), testCanary) || !strings.Contains(err.Error(), category) ||
		!strings.Contains(err.Error(), "test phase") {
		t.Fatal("audit error must identify only phase/category")
	}
}

func TestAuditArgumentSocket(t *testing.T) {
	audit := newLeakAudit()
	socket := filepath.Join(t.TempDir(), "argv.sock")
	stop, err := audit.listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	// The server must own and join its goroutine, including on test failures.
	defer func() {
		if closeErr := stop(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err = connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(connection).Encode([]string{"helm", "--set", "password=" + testCanary}); err != nil {
		t.Fatal(err)
	}
	var ack [1]byte
	if _, err = io.ReadFull(connection, ack[:]); err != nil {
		t.Fatal(err)
	}
	audit.add([]byte(testCanary))
	assertAuditLeak(t, audit, "subprocess arguments")
}
func TestAuditGeneratedSecretsAndPublicMetadata(t *testing.T) {
	secret := auditSecret{Data: map[string][]byte{
		"private-key":      []byte(testCanary),
		"cluster-id":       []byte("public-cluster-identity-00000000"),
		"tls.crt":          []byte("public-certificate-000000000000"),
		"ca.crt":           []byte("public-ca-certificate-00000000000"),
		"fingerprint":      []byte("public-fingerprint-000000000000"),
		"CONTROL_PLANE_ID": []byte("public-control-plane-00000000000"),
		"LOCO_API_URL":     []byte("https://public-control-plane.example"),
	}}
	secret.Metadata.Namespace = "loco-staging"
	audit := newLeakAudit()
	if err := audit.collectSecrets([]auditSecret{secret}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cluster-id", "tls.crt", "ca.crt", "fingerprint", "CONTROL_PLANE_ID", "LOCO_API_URL"} {
		audit.capture("CLI output", secret.Data[key])
	}
	if err := audit.check("metadata"); err != nil {
		t.Fatal(err)
	}
	audit.capture("CLI output", secret.Data["private-key"])
	assertAuditLeak(t, audit, "CLI output")
}
func TestAuditProtectedOperatorFiles(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "operator-first")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	// Ordinary authentication is expected; internal installation keys are not.
	file := filepath.Join(home, "credentials.json")
	if err := os.WriteFile(file, []byte("ordinary-user-session-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	audit := newLeakAudit()
	audit.add([]byte(testCanary))
	if err := audit.inspectFiles(root); err != nil {
		t.Fatal(err)
	}
	if err := audit.check("normal session"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(testCanary), 0600); err != nil {
		t.Fatal(err)
	}
	if err := audit.inspectFiles(root); err != nil {
		t.Fatal(err)
	}
	assertAuditLeak(t, audit, "operator files")
}

func TestAuditCredentialURLs(t *testing.T) {
	for _, key := range []string{"email-transport", "database-url", "DATABASE_URL", "url"} {
		t.Run(key, func(t *testing.T) {
			transport := []byte("https://notifications:" + testCanary + "@mail.example.test")
			secret := auditSecret{Data: map[string][]byte{key: transport}}
			secret.Metadata.Namespace = controlNamespace
			audit := newLeakAudit()
			if err := audit.collectSecrets([]auditSecret{secret}); err != nil {
				t.Fatal(err)
			}
			audit.capture("CLI output", []byte(testCanary))
			assertAuditLeak(t, audit, "CLI output")
		})
	}
}
func TestAuditBootstrapOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			fmt.Fprint(w, `<form action="/complete"><input name="login"><input name="password"></form>`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	for _, status := range []string{"0", "9"} {
		t.Run(status, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\nprintf '%s\\n' '" + server.URL + "/login' 'stdout-" + testCanary + "'\n" +
				"printf '%s' 'stderr-" + testCanary + "' >&2\nexit " + status + "\n"
			if err := os.WriteFile(filepath.Join(dir, "locoadm"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			audit := newLeakAudit()
			ctx := context.WithValue(t.Context(), auditKey{}, audit)
			err := bootstrap(ctx)
			if (err == nil) != (status == "0") {
				t.Fatal("unexpected bootstrap exit")
			}
			if err != nil && strings.Contains(err.Error(), testCanary) {
				t.Fatal("bootstrap error exposes canary")
			}
			audit.add([]byte("stdout-" + testCanary))
			assertAuditLeak(t, audit, "CLI output (stdout)")
			audit.canaries = nil
			audit.add([]byte("stderr-" + testCanary))
			assertAuditLeak(t, audit, "CLI output (stderr)")
		})
	}
}

func TestAuditInvalidHelmHistoryKeepsValuesPrivate(t *testing.T) {
	_, err := decodeRelease([]byte(testCanary))
	if err == nil || strings.Contains(err.Error(), testCanary) {
		t.Fatal("invalid release exposes data")
	}
}
func TestBoundedCommandOutput(t *testing.T) {
	var output boundedOutput
	data := make([]byte, auditLimit+1)
	written, err := output.Write(data)
	if err != nil || written != len(data) || len(output.Bytes()) != auditLimit || !output.exceeded {
		t.Fatal("command output must be bounded without blocking its child")
	}
}
