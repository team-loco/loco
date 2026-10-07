package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	registryv1 "github.com/team-loco/loco/gen/go/loco/registry/v1"
)

const registryTestServiceKey = "api"

func registryFixture(t *testing.T) (*RegistryServer, context.Context, *registryv1.GetImageRepositoryRequest) {
	t.Helper()
	machine := tvm.NewVendingMachine(nil, registryTestQueries{}, tvm.Config{})
	t.Cleanup(machine.Close)
	environment := uuid.New()
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, []db.EntityScope{
		{EntityType: db.EntityTypeEnvironment, EntityID: environment, Scope: db.ScopeWrite},
	})
	server := NewRegistryServer(nil, nil, machine, "", "", "", "registry.example.com/loco/images", nil)
	request := &registryv1.GetImageRepositoryRequest{
		EnvironmentId: environment.String(), StackName: "storefront", ServiceKey: registryTestServiceKey,
	}
	return server, ctx, request
}

func TestGetImageRepositoryIsEnvironmentScoped(t *testing.T) {
	server, ctx, request := registryFixture(t)
	response, err := server.GetImageRepository(ctx, connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	expected := "registry.example.com/loco/images/" + request.EnvironmentId + "/storefront.api"
	if response.Msg.GetRepository() != expected {
		t.Fatalf("repository = %q", response.Msg.GetRepository())
	}
}

func TestGetImageRepositoryRejectsForeignEnvironment(t *testing.T) {
	server, ctx, request := registryFixture(t)
	request.EnvironmentId = uuid.NewString()
	ctx = context.WithValue(ctx, contextkeys.EntityScopesKey, []db.EntityScope{})
	_, err := server.GetImageRepository(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected permission denied, got %v", err)
	}
}

func TestGetImageRepositoryRequiresAuthentication(t *testing.T) {
	server, _, request := registryFixture(t)
	_, err := server.GetImageRepository(context.Background(), connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("expected unauthenticated, got %v", err)
	}
}

type registryTestQueries struct{ db.Querier }

func (registryTestQueries) GetEnvironmentHierarchy(context.Context, uuid.UUID) (db.GetEnvironmentHierarchyRow, error) {
	return db.GetEnvironmentHierarchyRow{}, pgx.ErrNoRows
}
