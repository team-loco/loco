package builds

import (
	"testing"

	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func TestBuildOriginDoesNotBindItsImageToAnEnvironment(t *testing.T) {
	t.Parallel()
	const app, origin = "origin-build", "staging"
	w := p.NewTenant(t)
	created, err := p.User.Environment.CreateEnvironment(t.Context(), &environmentv1.CreateEnvironmentRequest{
		WorkspaceId: w.ID, Name: origin,
	})
	if err != nil {
		t.Fatal(err)
	}
	originID := created.GetEnvironmentId()
	dir := t.TempDir()
	p.CopyFixture(t, "build-app", dir)
	platform.WriteLocoFile(t, dir, locofile.File{
		Partial: app, Services: map[string]locofile.Service{app: platform.SourceService()},
	})
	w.MustLoco(t, dir, "deploy", "--env", origin, "--yes", "--wait")
	resourceID := w.ResourceID(t, app)
	builds, err := p.User.Build.ListBuilds(t.Context(), &buildv1.ListBuildsRequest{ResourceId: resourceID})
	if err != nil || len(builds.GetBuilds()) != 1 {
		t.Fatalf("initial builds = %v, error %v", builds, err)
	}
	build := builds.GetBuilds()[0]
	if build.GetOriginEnvironmentId() != originID || build.GetImageDigest() == "" {
		t.Fatalf("build origin %q, digest %q", build.GetOriginEnvironmentId(), build.GetImageDigest())
	}
	deployAndWait(t, w, resourceID, build.GetId(), "")
	image := build.GetImageRepository() + "@" + build.GetImageDigest()
	for _, environmentID := range []string{originID, w.EnvironmentID} {
		application, appErr := p.Application(t.Context(), resourceID, environmentID)
		if appErr != nil || application.Spec.ServiceSpec.Deployment.Image != image {
			t.Fatalf(
				"environment %s application = %v, error %v, want image %s",
				environmentID, application, appErr, image,
			)
		}
		deployments, deploymentErr := p.User.Deployment.ListDeployments(
			t.Context(),
			&deploymentv1.ListDeploymentsRequest{
				ResourceId: resourceID, EnvironmentId: environmentID,
			},
		)
		if deploymentErr != nil || len(deployments.GetDeployments()) != 1 ||
			deployments.GetDeployments()[0].GetSpec().GetService().GetBuild().GetBuildId() != build.GetId() {
			t.Fatalf("environment %s deployments = %v, error %v", environmentID, deployments, deploymentErr)
		}
	}
	production, err := p.User.Build.ListBuilds(t.Context(), &buildv1.ListBuildsRequest{
		ResourceId: resourceID, OriginEnvironmentId: new(w.EnvironmentID),
	})
	if err != nil || len(production.GetBuilds()) != 0 {
		t.Fatalf("production build history = %v, error %v; staging origin must stay distinct", production, err)
	}
	all, err := p.User.Build.ListBuilds(t.Context(), &buildv1.ListBuildsRequest{ResourceId: resourceID})
	if err != nil || len(all.GetBuilds()) != 1 || all.GetBuilds()[0].GetId() != build.GetId() {
		t.Fatalf("builds after reuse = %v, error %v; production must not rebuild", all, err)
	}
	w.MustLoco(t, dir, "builds", "get", build.GetId())
	w.MustLoco(t, dir, "builds", "logs", build.GetId())
}
