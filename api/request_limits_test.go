package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
)

func TestRPCReadLimit(t *testing.T) {
	const maximum = 1024
	for _, public := range []bool{true, false} {
		name := "authenticated"
		if public {
			name = "public"
		}
		t.Run(name, func(t *testing.T) {
			base := connect.NewServer()
			authenticated := connect.NewServer()
			server := authenticated
			if public {
				server = base
			}
			secretv1connect.RegisterSecretServiceHandler(server, &secretv1connect.UnimplementedSecretServiceHandler{})
			mux := http.NewServeMux()
			mountRPCServers(mux, base, authenticated, maximum)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			for _, compression := range []string{"", "gzip"} {
				transport := connecthttp.NewTransport(srv.Client(), srv.URL,
					connecthttp.WithSendCompression(compression), connecthttp.WithCompressMinBytes(0))
				client := secretv1connect.NewSecretServiceClient(connect.NewClient(transport))
				for _, size := range []int{1, maximum + 1} {
					_, err := client.SetSecrets(t.Context(), &secretv1.SetSecretsRequest{
						Values: map[string]string{"SECRET": strings.Repeat("a", size)},
					})
					want := connect.CodeUnimplemented
					if size > maximum {
						want = connect.CodeResourceExhausted
					}
					if got := connect.CodeOf(err); got != want {
						t.Errorf("value size %d: code = %v (%v), want %v", size, got, err, want)
					}
				}
			}
		})
	}
}
