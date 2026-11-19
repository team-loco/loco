package service

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/api/limits"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
)

func TestEnvironmentNamesAreDNSLabels(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	server := NewEnvironmentServer(f.pool, f.queries, limits.Defaults())
	workspaceID := f.workspace(t, "names").String()
	invalid := []string{"", "Staging EU", "staging_eu", "-staging", "staging-", strings.Repeat("a", 64)}

	for _, name := range invalid {
		_, err := server.CreateEnvironment(f.owner, &environmentv1.CreateEnvironmentRequest{
			WorkspaceId: workspaceID, Name: name,
		})
		if codeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("create %q: %v, want invalid argument", name, err)
		}
	}
	created, err := server.CreateEnvironment(f.owner, &environmentv1.CreateEnvironmentRequest{
		WorkspaceId: workspaceID, Name: "staging-eu-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range invalid {
		_, err = server.UpdateEnvironment(f.owner, &environmentv1.UpdateEnvironmentRequest{
			EnvironmentId: created.GetEnvironmentId(), Name: &name,
		})
		if codeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("rename to %q: %v, want invalid argument", name, err)
		}
	}
	if _, err = server.UpdateEnvironment(f.owner, &environmentv1.UpdateEnvironmentRequest{
		EnvironmentId: created.GetEnvironmentId(), Name: new("qa"),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err = f.pool.Exec(
		t.Context(), `UPDATE environments SET name = 'Staging EU' WHERE id = $1`, created.GetEnvironmentId(),
	); err == nil {
		t.Fatal("the database stored an environment name that is not a DNS label")
	}
}
