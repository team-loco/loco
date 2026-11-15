package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpcreflect/v2"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type publicConfig struct {
	configv1connect.UnimplementedConfigServiceHandler
}

func (*publicConfig) GetConfig(context.Context, *configv1.GetConfigRequest) (*configv1.GetConfigResponse, error) {
	return &configv1.GetConfigResponse{}, nil
}

func TestRPCServersKeepPublicSplitAndReflectBoth(t *testing.T) {
	base := connect.NewServer()
	rejecting := func(_ connect.ServerFunc) connect.ServerFunc {
		return func(context.Context, connect.Spec, connect.ServerStream) error {
			return connect.NewError(connect.CodeUnauthenticated, "token required")
		}
	}
	authenticated := connect.NewServer(rejecting)
	configv1connect.RegisterConfigServiceHandler(base, &publicConfig{})
	userv1connect.RegisterUserServiceHandler(authenticated, &userv1connect.UnimplementedUserServiceHandler{})
	mux := http.NewServeMux()
	mountRPCServers(mux, base, authenticated)
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	transport := connecthttp.NewTransport(srv.Client(), srv.URL)
	client := connect.NewClient(transport)
	configClient := configv1connect.NewConfigServiceClient(client)
	if _, err := configClient.GetConfig(t.Context(), &configv1.GetConfigRequest{}); err != nil {
		t.Fatal(err)
	}
	reflection := grpcreflect.NewClient(client)
	stream := reflection.NewStream(t.Context())
	defer stream.Close()
	_, err := stream.ListServices()
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("reflection bypassed auth: %v", err)
	}
}

func TestReflectionListsBothRPCServers(t *testing.T) {
	base := connect.NewServer()
	authenticated := connect.NewServer()
	configv1connect.RegisterConfigServiceHandler(base, &publicConfig{})
	userv1connect.RegisterUserServiceHandler(authenticated, &userv1connect.UnimplementedUserServiceHandler{})
	mux := http.NewServeMux()
	mountRPCServers(mux, base, authenticated)
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	transport := connecthttp.NewTransport(srv.Client(), srv.URL)
	client := grpcreflect.NewClient(connect.NewClient(transport))
	stream := client.NewStream(t.Context())
	defer stream.Close()
	names, err := stream.ListServices()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []protoreflect.FullName{configv1connect.ConfigServiceName, userv1connect.UserServiceName} {
		if !slices.Contains(names, want) {
			t.Errorf("reflection %v missing %s", names, want)
		}
	}
}
