package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
)

const testTLSAddr = ":8443"

func TestNewAPIConfigReadsTLSListener(t *testing.T) {
	certPEM, keyPEM, _ := selfSignedPEM(t)
	clearAPIConfigEnv(t)
	if addr := newAPIConfig().TLSListener.Addr; addr != "" {
		t.Fatalf("TLS listener without settings = %q, want none", addr)
	}
	t.Setenv("LOCO_TLS_ADDR", testTLSAddr)
	t.Setenv("LOCO_TLS_CERT", certPEM)
	t.Setenv("LOCO_TLS_KEY", keyPEM)
	listener := newAPIConfig().TLSListener
	if listener.Addr != testTLSAddr || listener.Certificate.Leaf == nil {
		t.Fatalf("TLS listener = %q with leaf %v", listener.Addr, listener.Certificate.Leaf)
	}
}

func TestNewAPIConfigRejectsInvalidTLSListener(t *testing.T) {
	certPEM, keyPEM, _ := selfSignedPEM(t)
	_, otherKeyPEM, _ := selfSignedPEM(t)
	tests := []struct {
		name            string
		addr, cert, key string
		want            error
	}{
		{"address only", testTLSAddr, "", "", errTLSListenerPartial},
		{"certificate without key", testTLSAddr, certPEM, "", errTLSListenerPartial},
		{"key and certificate without address", "", certPEM, keyPEM, errTLSListenerPartial},
		{"key only", "", "", keyPEM, errTLSListenerPartial},
		{"mismatched key", testTLSAddr, certPEM, otherKeyPEM, errInvalidTLSKeyPair},
		{"not PEM", testTLSAddr, "not a certificate", keyPEM, errInvalidTLSKeyPair},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAPIConfigEnv(t)
			t.Setenv("LOCO_TLS_ADDR", tt.addr)
			t.Setenv("LOCO_TLS_CERT", tt.cert)
			t.Setenv("LOCO_TLS_KEY", tt.key)
			if err := panicValue(t, func() { newAPIConfig() }); !errors.Is(err, tt.want) {
				t.Fatalf("panic = %v, want %v", err, tt.want)
			}
		})
	}
}

type echoSync struct {
	agentv1connect.UnimplementedAgentServiceHandler
}

func (echoSync) Sync(_ context.Context, stream agentv1connect.AgentServiceSyncServerStream) error {
	for {
		if _, err := stream.Receive(); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		if err := stream.Send(&agentv1.SyncResponse{}); err != nil {
			return err
		}
	}
}

func TestTLSServerServesSyncStreamOverHTTP2(t *testing.T) {
	certPEM, keyPEM, roots := selfSignedPEM(t)
	clearAPIConfigEnv(t)
	addr := freeAddr(t)
	t.Setenv("LOCO_TLS_ADDR", addr)
	t.Setenv("LOCO_TLS_CERT", certPEM)
	t.Setenv("LOCO_TLS_KEY", keyPEM)
	ac := newAPIConfig()

	agentServer := connect.NewServer()
	agentv1connect.RegisterAgentServiceHandler(agentServer, echoSync{})
	mux := http.NewServeMux()
	mountRPCServers(mux, agentServer, connect.NewServer())
	servers := newServers(ac, mux)
	if len(servers) != 2 {
		t.Fatalf("servers = %d, want plain and TLS", len(servers))
	}
	tlsServer := servers[1]
	served := make(chan error, 1)
	go func() { served <- serve(tlsServer) }()
	t.Cleanup(func() {
		if closeErr := tlsServer.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		if serveErr := <-served; serveErr != nil {
			t.Error(serveErr)
		}
	})
	waitListening(t, addr)
	url := "https://" + addr

	h2 := new(http.Protocols)
	h2.SetHTTP2(true)
	client := agentv1connect.NewAgentServiceClient(connect.NewClient(
		connecthttp.NewTransport(tlsClient(roots, h2), url),
	))
	stream, err := client.Sync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for range 2 {
		if sendErr := stream.Send(&agentv1.SyncRequest{}); sendErr != nil {
			t.Fatalf("send: %v", sendErr)
		}
		if _, receiveErr := stream.Receive(); receiveErr != nil {
			t.Fatalf("receive before closing the request side: %v", receiveErr)
		}
	}

	h1 := new(http.Protocols)
	h1.SetHTTP1(true)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := tlsClient(roots, h1).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.ProtoMajor != 1 {
		t.Fatalf("HTTP/1.1 client got %s", response.Proto)
	}
}

// freeAddr returns a loopback address with a port nothing listens on, for serve to listen on.
func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never started listening: %v", addr, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func tlsClient(roots *x509.CertPool, protocols *http.Protocols) *http.Client {
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		Protocols:       protocols,
	}}
}

// selfSignedPEM returns a certificate for 127.0.0.1, its key, and a pool that trusts it.
func selfSignedPEM(t *testing.T) (certPEM, keyPEM string, roots *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "loco-api-test"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots = x509.NewCertPool()
	roots.AddCert(cert)
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM, roots
}
