package service

import (
	"errors"
	"slices"
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
		for _, deleteFirst := range []bool{false, true} {
			order := "outcome first"
			if deleteFirst {
				order = "delete first"
			}
			t.Run(name+"/"+order, func(t *testing.T) {
				t.Parallel()
				f := newBuildFixture(t)
				buildID := f.queued(t)
				deployBuild(t, f, buildID)
				workspaces := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
				admin := reqctx.WithPrincipal(t.Context(), reqctx.Principal{UserID: f.userID, Scopes: f.adminScopes(t)})

				release := sync.OnceFunc(f.holdResourceLock(t))
				t.Cleanup(release)
				var buildErr, deleteErr error
				writers := []func(){
					func() { buildErr = finish(f, buildID) },
					func() {
						_, deleteErr = workspaces.DeleteWorkspace(admin, &workspacev1.DeleteWorkspaceRequest{
							WorkspaceId: f.workspaceID(t).String(), ConfirmDeleteApps: true,
						})
					},
				}
				if deleteFirst {
					slices.Reverse(writers)
				}
				var running sync.WaitGroup
				for i, write := range writers {
					running.Go(write)
					dbtest.WaitForLockWaiters(t, f.pool, i+1)
				}
				release()
				running.Wait()
				if deleteFirst && errors.Is(buildErr, ErrResourceNotFound) {
					buildErr = nil
				}
				if buildErr != nil || deleteErr != nil {
					t.Fatalf("build outcome: %v, delete workspace: %v", buildErr, deleteErr)
				}
				if n := deadlocks(t, f.pool); n != 0 {
					t.Fatalf("Postgres broke %d deadlocks", n)
				}
			})
		}
	}
}
