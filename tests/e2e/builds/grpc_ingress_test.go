package builds

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func TestGRPCIngress(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const name = "grpc-ingress"
	host := platform.Host(name)
	dir := t.TempDir()
	p.CopyFixture(t, "grpc-app", filepath.Join(dir, name))
	platform.WriteLocoFile(t, dir, locofile.File{Partial: name, Services: map[string]locofile.Service{
		name: {
			Context: name, Dockerfile: buildAppDockerfile, Port: new(int32(50051)),
			Domains: []string{host}, Regions: map[string]locofile.Region{p.Region: platform.OneReplica()},
		},
	}})
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")

	gateway, err := p.GatewayService(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	port := p.PortForward(t, p.Namespace, "service/"+gateway, 443)
	certificate, err := base64.StdEncoding.DecodeString(p.MustKubectl(t, "-n", p.Namespace,
		"get", "secret", "e2e-gateway-tls", "-o", "jsonpath={.data.tls\\.crt}"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		t.Fatal("gateway certificate is not PEM")
	}
	conn, err := grpc.NewClient("passthrough:///"+host+":443",
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	client := grpc_health_v1.NewHealthClient(conn)
	request := &grpc_health_v1.HealthCheckRequest{Service: "echo"}
	platform.Eventually(t, time.Minute, "a unary gRPC call through the TLS gateway", func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var trailers metadata.MD
		response, callErr := client.Check(ctx, request, grpc.Trailer(&trailers))
		if callErr != nil {
			return callErr
		}
		if response.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
			return fmt.Errorf("response: %v", response)
		}
		return checkGRPCMethod(trailers, grpc_health_v1.Health_Check_FullMethodName)
	})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var trailers metadata.MD
	_, err = client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: "missing"}, grpc.Trailer(&trailers))
	if status.Code(err) != codes.NotFound || status.Convert(err).Message() != "unknown service: missing" {
		t.Fatalf("error status = %v, want NotFound with the backend message", err)
	}
	if err = checkGRPCMethod(trailers, grpc_health_v1.Health_Check_FullMethodName); err != nil {
		t.Fatal(err)
	}
	stream, err := client.Watch(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []grpc_health_v1.HealthCheckResponse_ServingStatus{
		grpc_health_v1.HealthCheckResponse_SERVING, grpc_health_v1.HealthCheckResponse_NOT_SERVING,
	} {
		response, receiveErr := stream.Recv()
		if receiveErr != nil || response.GetStatus() != want {
			t.Fatalf("stream response = %v (%v), want %s", response, receiveErr, want)
		}
	}
	if _, receiveErr := stream.Recv(); !errors.Is(receiveErr, io.EOF) {
		t.Fatalf("stream completion = %v, want EOF (gRPC OK)", receiveErr)
	}
	if err = checkGRPCMethod(stream.Trailer(), grpc_health_v1.Health_Watch_FullMethodName); err != nil {
		t.Fatal(err)
	}
	// gRPC's explicit protobuf media type takes the same HTTP/2 route.
	if _, err = client.Check(ctx, request, grpc.CallContentSubtype("proto")); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", port))
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Transport: transport}).Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Error(closeErr)
	}
	if err != nil || response.StatusCode != http.StatusOK || response.ProtoMajor != 2 || string(body) != "HTTP/1.1" {
		t.Fatalf("ordinary HTTP through the same gateway: status=%d protocol=%s body=%q err=%v",
			response.StatusCode, response.Proto, body, err)
	}
}

func checkGRPCMethod(trailers metadata.MD, want string) error {
	if got := trailers.Get("fixture-method"); len(got) != 1 || got[0] != want {
		return fmt.Errorf("method trailer = %v, want %s", got, want)
	}
	return nil
}
