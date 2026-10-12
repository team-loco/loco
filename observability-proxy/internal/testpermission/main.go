package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
)

const (
	listenAddress     = "127.0.0.1:0"
	addressFileMode   = 0o600
	readHeaderTimeout = time.Second
)

var (
	errMissingInputs      = errors.New("token and address-file are required")
	errCredentialRejected = errors.New("fixture credential rejected")
)

type permissionService struct {
	tokenv1connect.UnimplementedTokenServiceHandler
	authToken string
}

func (s *permissionService) CheckPermission(
	_ context.Context,
	req *connect.Request[tokenv1.CheckPermissionRequest],
) (*connect.Response[tokenv1.CheckPermissionResponse], error) {
	if req.Header().Get("Authorization") != "Bearer "+s.authToken {
		return nil, connect.NewError(connect.CodeUnauthenticated, errCredentialRejected)
	}
	response := &tokenv1.CheckPermissionResponse{Allowed: false}
	return connect.NewResponse(response), nil
}

func main() {
	authToken := flag.String("token", "", "fixture API credential")
	addressFile := flag.String("address-file", "", "file for the listening URL")
	flag.Parse()
	if *authToken == "" || *addressFile == "" {
		log.Fatal(errMissingInputs)
	}
	ctx := context.Background()
	var listenerConfig net.ListenConfig
	listener, err := listenerConfig.Listen(ctx, "tcp", listenAddress)
	if err != nil {
		log.Fatal(err)
	}
	service := &permissionService{authToken: *authToken}
	path, handler := tokenv1connect.NewTokenServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}
	address := "http://" + listener.Addr().String()
	addressBytes := []byte(address)
	if err := os.WriteFile(*addressFile, addressBytes, addressFileMode); err != nil {
		log.Fatal(err)
	}
	serveErr := server.Serve(listener)
	log.Fatal(serveErr)
}
