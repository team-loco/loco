package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireTLS13(t *testing.T) {
	for _, minimum := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		t.Run(tls.VersionName(minimum), func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.TLS = &tls.Config{MinVersion: minimum}
			server.StartTLS()
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			err := requireTLS13(t.Context(), server.Listener.Addr().String(), "example.com", roots)
			if minimum == tls.VersionTLS12 && !errors.Is(err, errTLS12Accepted) {
				t.Fatalf("expected accepted TLS 1.2, got %v", err)
			}
			if minimum == tls.VersionTLS13 && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRequireTLS13DoesNotAcceptCertificateFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	if err := requireTLS13(
		t.Context(),
		server.Listener.Addr().String(),
		"wrong.example",
		x509.NewCertPool(),
	); err == nil {
		t.Fatal("certificate failure passed TLS minimum check")
	}
}
