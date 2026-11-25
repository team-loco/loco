package service

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestKeptWorkspacesFollowThePlacementGrace(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	workspaceID := f.workspaceID(t).String()
	if err := f.queries.OpenPlacementWindow(ctx, genDb.OpenPlacementWindowParams{
		ClusterID: f.clusterID, ResourceID: f.resourceID,
	}); err != nil {
		t.Fatal(err)
	}
	session, sent := recordingSession(f)
	// The unknown workspace makes every inventory send the kept workspaces.
	inventory := &agentv1.Inventory{WorkspaceIds: []string{workspaceID, uuid.NewString()}}
	kept := func(want bool) {
		t.Helper()
		if err := session.reconcileInventory(ctx, inventory); err != nil {
			t.Fatal(err)
		}
		ids, _ := lastKeptWorkspaces(t, *sent)
		if slices.Contains(ids, workspaceID) != want {
			t.Fatalf("kept workspaces %v, want the workspace kept %v", ids, want)
		}
	}
	kept(true)
	removeWindow := func(removedAgo string) {
		t.Helper()
		const update = "UPDATE placement_windows " +
			"SET placed_at = NOW() - interval '1 day', removed_at = NOW() - $2::interval WHERE cluster_id = $1"
		if _, err := f.pool.Exec(ctx, update, f.clusterID, removedAgo); err != nil {
			t.Fatal(err)
		}
	}
	removeWindow("1 minute")
	kept(true)
	removeWindow("2 hours")
	kept(false)
}

func TestKeptWorkspacesLeaveOutADeletedWorkspace(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatal(err)
	}
	workspaceID := f.workspaceID(t)
	admin := reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeAdmin},
	}})
	_, err := NewWorkspaceServer(f.pool, f.queries, limits.Defaults()).DeleteWorkspace(
		admin,
		&workspacev1.DeleteWorkspaceRequest{WorkspaceId: workspaceID.String(), ConfirmDeleteApps: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	session, sent := recordingSession(f)
	if err := session.sendKeptWorkspaces(ctx, []string{workspaceID.String()}); err != nil {
		t.Fatal(err)
	}
	if ids, _ := lastKeptWorkspaces(t, *sent); len(ids) != 0 {
		t.Fatalf("kept workspaces %v after the workspace was deleted, want none", ids)
	}
}

func TestKeptWorkspacesAreNotSentWhenTheQueryFails(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	closed, err := pgxpool.NewWithConfig(t.Context(), f.pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	session, sent := recordingSession(f)
	session.server.db = closed
	session.server.queries = genDb.New(closed)
	if err := session.sendKeptWorkspaces(t.Context(), []string{uuid.NewString()}); err == nil {
		t.Fatal("a failed workspace query returned nil")
	}
	if len(*sent) != 0 {
		t.Fatalf("a failed workspace query sent %v", *sent)
	}
}

func TestKeptWorkspacesCoverEveryApplySentBeforeThem(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatal(err)
	}
	session, sent := recordingSession(f)
	inventory := &agentv1.Inventory{WorkspaceIds: []string{uuid.NewString()}}
	if err := session.reconcileInventory(ctx, inventory); err != nil {
		t.Fatal(err)
	}
	ids, at := lastKeptWorkspaces(t, *sent)
	applied := slices.IndexFunc(*sent, func(msg *agentv1.SyncResponse) bool { return msg.GetApply() != nil })
	if applied < 0 || applied > at {
		t.Fatalf("sent %v, want the placement's Apply before the kept workspaces", *sent)
	}
	if !slices.Contains(ids, f.workspaceID(t).String()) {
		t.Fatalf("kept workspaces %v leave out the workspace of an Apply already sent", ids)
	}
}

func TestKeptWorkspacesAreNotSentWhileTheClusterHoldsNothingElse(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatal(err)
	}
	session, sent := recordingSession(f)
	inventory := &agentv1.Inventory{WorkspaceIds: []string{f.workspaceID(t).String()}}
	if err := session.reconcileInventory(ctx, inventory); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(*sent, func(msg *agentv1.SyncResponse) bool { return msg.GetKeepWorkspaces() != nil }) {
		t.Fatalf("sent %v for a cluster holding only kept workspaces", *sent)
	}
}

func recordingSession(f *deployFixture) (*syncSession, *[]*agentv1.SyncResponse) {
	sent := &[]*agentv1.SyncResponse{}
	session := newSyncSession(f.agentServer(), f.clusterID, func(msg *agentv1.SyncResponse) error {
		*sent = append(*sent, msg)
		return nil
	})
	return session, sent
}

// lastKeptWorkspaces returns the workspace IDs of the last kept-workspaces message and its index in sent.
func lastKeptWorkspaces(t *testing.T, sent []*agentv1.SyncResponse) ([]string, int) {
	t.Helper()
	for i, msg := range slices.Backward(sent) {
		if kept := msg.GetKeepWorkspaces(); kept != nil {
			return kept.GetWorkspaceIds(), i
		}
	}
	t.Fatalf("sent %v, want a kept-workspaces message", sent)
	return nil, 0
}
