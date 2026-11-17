package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type healthServer struct {
	grpc_health_v1.UnimplementedHealthServer
}

func main() {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", ":50051")
	if err != nil {
		log.Fatal(err)
	}
	server := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer{})
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	httpServer := &http.Server{
		Protocols:         protocols,
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
				server.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			protocol := "HTTP/1.1"
			if r.ProtoMajor == 2 {
				protocol = "HTTP/2.0"
			}
			if _, err := w.Write([]byte(protocol)); err != nil {
				log.Print(err)
			}
		}),
	}
	if err := httpServer.Serve(listener); err != nil {
		log.Fatal(err)
	}
}

func (healthServer) Check(
	ctx context.Context,
	request *grpc_health_v1.HealthCheckRequest,
) (*grpc_health_v1.HealthCheckResponse, error) {
	method, _ := grpc.Method(ctx)
	if err := grpc.SetTrailer(ctx, metadata.Pairs("fixture-method", method)); err != nil {
		return nil, err
	}
	if request.GetService() != "echo" {
		return nil, status.Error(codes.NotFound, "unknown service: "+request.GetService())
	}
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

// Watch deliberately ends after two messages so the gateway test can verify stream completion.
func (healthServer) Watch(
	_ *grpc_health_v1.HealthCheckRequest,
	stream grpc.ServerStreamingServer[grpc_health_v1.HealthCheckResponse],
) error {
	method, _ := grpc.Method(stream.Context())
	stream.SetTrailer(metadata.Pairs("fixture-method", method))
	for _, serving := range []grpc_health_v1.HealthCheckResponse_ServingStatus{
		grpc_health_v1.HealthCheckResponse_SERVING,
		grpc_health_v1.HealthCheckResponse_NOT_SERVING,
	} {
		if err := stream.Send(&grpc_health_v1.HealthCheckResponse{Status: serving}); err != nil {
			return err
		}
	}
	return nil
}
