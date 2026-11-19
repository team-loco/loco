package service

import (
	"encoding/json"
	"testing"

	"github.com/team-loco/loco/api/limits"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

const qaEnvironment = "qa-env"

func TestUpdateAndDeleteEventsRecordTheNameOfTheirSubject(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	workspaces := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	environments := NewEnvironmentServer(f.pool, f.queries, limits.Defaults())
	workspaceID := f.workspace(t, "doomed")
	created, err := environments.CreateEnvironment(f.owner, &environmentv1.CreateEnvironmentRequest{
		WorkspaceId: workspaceID.String(), Name: qaEnvironment,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = environments.UpdateEnvironment(f.owner, &environmentv1.UpdateEnvironmentRequest{
		EnvironmentId: created.GetEnvironmentId(), DisplayName: new("QA renamed"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = environments.DeleteEnvironment(f.owner, &environmentv1.DeleteEnvironmentRequest{
		EnvironmentId: created.GetEnvironmentId(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = workspaces.UpdateWorkspace(f.owner, &workspacev1.UpdateWorkspaceRequest{
		WorkspaceId: workspaceID.String(), Name: new("audit-renamed"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = workspaces.DeleteWorkspace(f.owner, &workspacev1.DeleteWorkspaceRequest{
		WorkspaceId: workspaceID.String(),
	}); err != nil {
		t.Fatal(err)
	}
	for eventType, want := range map[string]string{
		"environment.updated": qaEnvironment,
		"environment.deleted": qaEnvironment,
		"workspace.updated":   "audit-renamed",
		"workspace.deleted":   "audit-renamed",
	} {
		var raw []byte
		const latest = "SELECT data FROM events WHERE type = $1 AND workspace_id = $2 ORDER BY seq DESC LIMIT 1"
		if err = f.pool.QueryRow(t.Context(), latest, eventType, workspaceID).Scan(&raw); err != nil {
			t.Fatalf("%s: %v", eventType, err)
		}
		var data map[string]any
		if err = json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		if data["name"] != want {
			t.Errorf("%s name = %v, want %s", eventType, data["name"], want)
		}
	}
}
