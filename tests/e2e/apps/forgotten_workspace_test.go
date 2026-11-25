package apps

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/k8sapi/labels"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const forgottenTimeout = 3 * time.Minute

// TestForgottenWorkspaceLeavesTheCluster removes a running workspace and its placements from the
// control plane's database, as a database reset does, and checks the cluster deletes its Application
// and namespace while a namespace without Loco's labels stays.
func TestForgottenWorkspaceLeavesTheCluster(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-forgotten"
	resourceID := deployForgotten(t, w, app)

	unowned := "e2e-unowned-" + strings.ToLower(resourceID[len(resourceID)-8:])
	p.MustKubectl(t, "create", "namespace", unowned)
	p.DeleteOnCleanup(t, "namespace", unowned)

	err := pgx.BeginFunc(t.Context(), p.DB, func(tx pgx.Tx) error {
		for _, statement := range []string{
			"DELETE FROM placements WHERE resource_id IN (SELECT id FROM resources WHERE workspace_id = $1)",
			"DELETE FROM resources WHERE workspace_id = $1",
			"DELETE FROM workspaces WHERE id = $1",
		} {
			if _, err := tx.Exec(t.Context(), statement, w.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("forget the workspace: %v", err)
	}

	const what = "the cluster to delete the forgotten workspace"
	platform.Eventually(t, forgottenTimeout, what, func(ctx context.Context) error {
		apps, err := p.Exists(ctx, "applications", "-A", "-l", labels.LabelResourceID+"="+resourceID)
		if err != nil {
			return err
		}
		if apps {
			return errors.New("the Application still exists")
		}
		namespace, err := p.Exists(ctx, "namespace", w.Namespace)
		if err != nil {
			return err
		}
		if namespace {
			return errors.New("the workspace namespace still exists")
		}
		return nil
	})
	if found, err := p.Exists(t.Context(), "namespace", unowned); err != nil || !found {
		t.Errorf("namespace %s without Loco's labels: found %v, err %v", unowned, found, err)
	}
}

// deployForgotten deploys app in w and returns its resource ID.
func deployForgotten(t *testing.T, w *platform.Workspace, app string) string {
	t.Helper()
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, locofile.File{
		Partial:  app,
		Services: map[string]locofile.Service{app: platform.ImageService(p.PublicImage)},
	})
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	return w.ResourceID(t, app)
}
