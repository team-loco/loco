package service

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestBuildOutcomeAndDeleteWorkspaceDoNotDeadlock(t *testing.T) {
	t.Parallel()
	for name, finish := range map[string]func(*buildFixture, string) error{
		"cancel": func(f *buildFixture, buildID string) error {
			_, err := f.cancel(buildID)
			return err
		},
		"failed report": func(f *buildFixture, buildID string) error {
			status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_FAILED)
			return f.agentServer().recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status)
		},
		"succeeded report": func(f *buildFixture, buildID string) error {
			status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
			status.ImageDigest = testImageDigest
			return f.agentServer().recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newBuildFixture(t)
			buildID := f.queued(t)
			deployBuild(t, f, buildID)
			workspaces := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
			admin := reqctx.WithPrincipal(t.Context(), reqctx.Principal{UserID: f.userID, Scopes: f.adminScopes(t)})

			release := sync.OnceFunc(f.holdResourceLock(t))
			t.Cleanup(release)
			var writers sync.WaitGroup
			var buildErr, deleteErr error
			writers.Go(func() { buildErr = finish(f, buildID) })
			dbtest.WaitForLockWaiters(t, f.pool, 1)
			writers.Go(func() {
				_, deleteErr = workspaces.DeleteWorkspace(admin, &workspacev1.DeleteWorkspaceRequest{
					WorkspaceId: f.workspaceID(t).String(), ConfirmDeleteApps: true,
				})
			})
			dbtest.WaitForLockWaiters(t, f.pool, 2)
			release()
			writers.Wait()
			if buildErr != nil || deleteErr != nil {
				t.Fatalf("build outcome: %v, delete workspace: %v", buildErr, deleteErr)
			}
			if n := deadlocks(t, f.pool); n != 0 {
				t.Fatalf("Postgres broke %d deadlocks", n)
			}
		})
	}
}
