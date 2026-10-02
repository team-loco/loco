package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/gen/db"
	registryv1 "github.com/team-loco/loco/gen/go/loco/registry/v1"
)

func authenticatedContext() context.Context {
	entity := db.Entity{Type: db.EntityTypeUser, ID: uuid.Must(uuid.NewV7())}
	return context.WithValue(context.Background(), contextkeys.EntityKey, entity)
}

func TestGetImageRepositoryReturnsConfiguredRepository(t *testing.T) {
	const repository = "registry.example.com/loco/images"
	server := NewRegistryServer(nil, nil, "", "", "", repository, nil, nil)
	req := connect.NewRequest(&registryv1.GetImageRepositoryRequest{})

	resp, err := server.GetImageRepository(authenticatedContext(), req)
	if err != nil {
		t.Fatalf("GetImageRepository: %v", err)
	}
	if got := resp.Msg.GetRepository(); got != repository {
		t.Fatalf("repository = %q, want %q", got, repository)
	}
}

func TestGetImageRepositoryWithoutRepositoryConfigured(t *testing.T) {
	server := NewRegistryServer(nil, nil, "", "", "", "", nil, nil)
	req := connect.NewRequest(&registryv1.GetImageRepositoryRequest{})

	_, err := server.GetImageRepository(authenticatedContext(), req)
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want %v (err %v)", code, connect.CodeFailedPrecondition, err)
	}
}

func TestGetImageRepositoryRequiresAnEntity(t *testing.T) {
	server := NewRegistryServer(nil, nil, "", "", "", "registry.example.com/loco/images", nil, nil)
	req := connect.NewRequest(&registryv1.GetImageRepositoryRequest{})

	_, err := server.GetImageRepository(context.Background(), req)
	if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want %v (err %v)", code, connect.CodeUnauthenticated, err)
	}
}
