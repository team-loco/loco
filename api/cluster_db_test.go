package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/auth/authtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/service"
)

func TestClusterCommandsWithDatabase(t *testing.T) {
	pool := authtest.NewPool(t)
	ctx := t.Context()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO clusters (name, region, provider, is_active, is_default)
VALUES ('operator-test', 'east', 'kubernetes', true, false) RETURNING id`).
		Scan(&id); err != nil {
		t.Fatal(err)
	}
	databaseURL := pool.Config().ConnString()
	var output bytes.Buffer
	if err := runClusterCommand(ctx, databaseURL, []string{clusterList}, &output); err != nil {
		t.Fatal(err)
	}
	var listed []genDb.ListOperatorClustersRow
	if err := json.Unmarshal(output.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != id {
		t.Fatalf("listed clusters: %+v", listed)
	}
	for _, operation := range []string{service.ClusterPause, service.ClusterRemove} {
		output.Reset()
		if err := runClusterCommand(ctx, databaseURL, []string{operation, id.String()}, &output); err != nil {
			t.Fatal(err)
		}
		var result service.ClusterOperationResult
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.ClusterID != id || result.Operation != operation {
			t.Fatalf("operation output: %+v", result)
		}
	}
	if err := runClusterCommand(
		ctx,
		databaseURL,
		[]string{service.ClusterResume, id.String()},
		&output,
	); !errors.Is(
		err,
		service.ErrClusterRemoved,
	) {
		t.Fatalf("resume removed cluster: %v", err)
	}
	for _, operation := range []string{service.ClusterRestore, service.ClusterResume} {
		if err := runClusterCommand(ctx, databaseURL, []string{operation, id.String()}, &output); err != nil {
			t.Fatal(err)
		}
	}
}
