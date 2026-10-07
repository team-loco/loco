package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func scaleResource(t *testing.T, f *deployFixture, replicas int32) error {
	t.Helper()
	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{LastUsedUpdateInterval: time.Minute})
	t.Cleanup(machine.Close)
	server := NewResourceServer(f.pool, f.queries, machine, testServiceDefaults())
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	req := connect.NewRequest(&resourcev1.ScaleResourceRequest{
		ResourceId: f.resourceID.String(),
		Replicas:   &replicas,
	})
	_, err := server.ScaleResource(ctx, req)
	return err
}

func TestScaleResourceRejectsReplicasTheControllerRejects(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	err := scaleResource(t, f, 15)
	wantCode(t, err, connect.CodeInvalidArgument)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 1 {
		t.Fatalf("%d deployments after a rejected scale, want 1", n)
	}
	replicas := desiredPayload(t, f).AppSpec.ServiceSpec.Resources.Replicas
	if replicas.Min != 1 || replicas.Max != 1 {
		t.Fatalf("desired replicas = %+v, want the original 1-1", replicas)
	}
}

func TestScaleResourceWritesTheReplicasIntoTheDesiredSpec(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	const scaled = 4
	if err := scaleResource(t, f, scaled); err != nil {
		t.Fatalf("scale: %v", err)
	}
	replicas := desiredPayload(t, f).AppSpec.ServiceSpec.Resources.Replicas
	if replicas.Min != scaled || replicas.Max != scaled {
		t.Fatalf("desired replicas = %+v, want %d-%d", replicas, scaled, scaled)
	}
}
