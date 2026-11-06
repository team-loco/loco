package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRecoveryForwardPreservesTLSIdentityAndAuthentication(t *testing.T) {
	for _, name := range []string{"example.com", "wrong.invalid"} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/sys/seal-status" && r.Header.Get("X-Vault-Token") != "forward-token-canary" {
					t.Error("forward lost OpenBao authentication")
					http.Error(w, "response-secret-canary", http.StatusForbidden)
					return
				}
				if _, err := io.WriteString(w, `{}`); err != nil {
					t.Error(err)
				}
			}))
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.StartTLS()
			defer server.Close()
			remote, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			fakeOperatorCommand(t, `#!/bin/sh
exec "$BAO_FORWARD_TEST_BINARY" -test.run=TestBaoForwardSubprocess -- "$@"
`)
			t.Setenv("BAO_FORWARD_TEST_BINARY", executable)
			t.Setenv("BAO_FORWARD_TEST_BACKEND", remote.Host)
			t.Setenv("BAO_FORWARD_TEST_PORT", remote.Port())
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			client := baoClient{
				client: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
					RootCAs: roots, ServerName: name, MinVersion: tls.VersionTLS12,
				}}},
				endpoint: "https://example.com:" + remote.Port(), token: "forward-token-canary",
			}
			ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
			defer cancel()
			process, endpoint, err := forwardBao(ctx, client, controlNamespace, "service/"+sourceBao)
			if process != nil {
				defer func() {
					client.client.CloseIdleConnections()
					if stopErr := stopProcess(process); stopErr != nil {
						t.Error(stopErr)
					}
					if process.ProcessState == nil {
						t.Error("forward process was not joined")
					}
				}()
			}
			if name != "example.com" {
				if !errors.Is(err, errRecoveryDrill) {
					t.Fatalf("wrong TLS server identity accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("TLS forward failed: %v", err)
			}
			client.endpoint = endpoint
			if _, err := client.request(ctx, http.MethodGet, "auth/token/lookup-self", nil); err != nil {
				t.Fatalf("authenticated forwarded request failed: %v", err)
			}
		})
	}
}

func TestBaoForwardSubprocess(t *testing.T) {
	backend := os.Getenv("BAO_FORWARD_TEST_BACKEND")
	if backend == "" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		t.Fatal("missing forward argv")
	}
	args := os.Args[separator+1:]
	if len(args) != 6 || args[0] != "-n" || args[1] != controlNamespace || args[2] != "port-forward" ||
		args[3] != "service/"+sourceBao || args[5] != "--address=127.0.0.1" {
		t.Fatal("wrong forward target")
	}
	local, remote, ok := strings.Cut(args[4], ":")
	if !ok || remote != os.Getenv("BAO_FORWARD_TEST_PORT") {
		t.Fatal("wrong forward remote port")
	}
	port, err := strconv.Atoi(local)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connection, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	// #nosec G704 -- The parent test owns this loopback TLS listener and its proxy subprocess.
	upstream, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", backend)
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, copyErr := io.Copy(upstream, connection); copyErr != nil && !errors.Is(copyErr, net.ErrClosed) {
			t.Error(copyErr)
		}
		if closeErr := upstream.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if _, copyErr := io.Copy(connection, upstream); copyErr != nil && !errors.Is(copyErr, net.ErrClosed) {
		t.Error(copyErr)
	}
	if closeErr := connection.Close(); closeErr != nil {
		t.Error(closeErr)
	}
	<-done
}

func TestRecoveryFixtureRendersListenerAndAdvertisedPort(t *testing.T) {
	rendered, err := renderFixtureFile(
		filepath.Join("..", "..", "tests", "e2e", "fixtures", "installation", "recovery.yaml"),
		map[string]any{
			"Namespace": "restore",
			"Pod":       "restore",
			"BaoImage":  "bao",
			"KeyID":     "restore-key-id",
			"Seal":      "fixture-seal",
			"Port":      "12345",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{`api_addr = "http://127.0.0.1:12345"`, `address = "127.0.0.1:12345"`} {
		if !strings.Contains(string(rendered), address) {
			t.Fatal("restore fixture did not use the supplied listener port")
		}
	}
}
