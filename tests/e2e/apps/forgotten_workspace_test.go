package apps

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/k8sapi/labels"
	"github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const forgottenTimeout = 3 * time.Minute

// TestForgottenWorkspaceLeavesTheCluster removes a running workspace from the control plane's
// database, as a database reset does, and checks the cluster deletes its Application and namespace
// while a namespace without Loco's labels stays.
func TestForgottenWorkspaceLeavesTheCluster(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-forgotten"
	resourceID := deployForgotten(t, w, app)

	unowned := "e2e-unowned-" + strings.ToLower(resourceID[len(resourceID)-8:])
	p.MustKubectl(t, "create", "namespace", unowned)
	p.DeleteOnCleanup(t, "namespace", unowned)

	err := pgx.BeginFunc(t.Context(), p.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), "DELETE FROM placements WHERE resource_id = $1", resourceID); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), "DELETE FROM workspaces WHERE id = $1", w.ID)
		return err
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

// TestForgottenPlacementLeavesItsReplacementServing forgets a running placement and deploys the same
// service again, which creates a new placement for the same resource and environment, and checks
// the cluster deletes the forgotten Application while the new one keeps serving.
func TestForgottenPlacementLeavesItsReplacementServing(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-replaced"
	resourceID := deployForgotten(t, w, app)

	var forgotten string
	err := p.DB.QueryRow(t.Context(), "DELETE FROM placements WHERE resource_id = $1 RETURNING id", resourceID).
		Scan(&forgotten)
	if err != nil {
		t.Fatalf("forget the placement: %v", err)
	}
	created, err := p.User.Deployment.CreateDeployment(t.Context(), &deploymentv1.CreateDeploymentRequest{
		ResourceId:    resourceID,
		EnvironmentId: w.EnvironmentID,
		Regions:       []string{p.Region},
		Spec: &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: &deploymentv1.ServiceDeploymentSpec{
				Build: &deploymentv1.BuildSource{Type: imageBuild, Image: p.PublicImage},
				Port:  new(int32(8080)),
			}},
		},
	})
	if err != nil {
		t.Fatalf("deploy again: %v", err)
	}

	name := v1alpha1.ApplicationName(forgotten)
	platform.Eventually(t, forgottenTimeout, "the cluster to delete "+name, func(ctx context.Context) error {
		found, err := p.Exists(ctx, "applications", "-A", "--field-selector", "metadata.name="+name)
		if err != nil {
			return err
		}
		if found {
			return errors.New("the forgotten Application still exists")
		}
		return nil
	})
	waitServing(t, w, app, created.GetDeploymentId())
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
