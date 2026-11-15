package apps

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

// TestBuildsDisabled checks an apps-only install: no build objects, an apps cluster role, public
// images deploy, source deploys are refused with advice, --image skips the build, and a workspace
// API token deploys without a session.
func TestBuildsDisabled(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)

	t.Run("no build objects are installed", func(t *testing.T) {
		for _, object := range [][]string{
			{"crd", "builds.infra.loco.io"},
			{"namespace", p.BuildNamespace},
			{"-n", p.Namespace, "deployment", "loco-build-controller"},
			{"clusterrole", "loco-build-controller"},
		} {
			if found, err := p.Exists(t.Context(), object...); err != nil || found {
				t.Errorf("%s exists (%v)", strings.Join(object, " "), err)
			}
		}
		p.MustKubectl(t, "-n", p.Namespace, "rollout", "status", "deployment/loco-controller", "--timeout", "60s")
		var pods corev1.PodList
		p.MustGet(t, &pods, "pods", "-A")
		for _, pod := range pods.Items {
			for _, container := range pod.Spec.Containers {
				if strings.Contains(container.Image, "loco-builder") || strings.Contains(container.Image, "buildkit") {
					t.Errorf("pod %s/%s runs %s", pod.Namespace, pod.Name, container.Image)
				}
			}
		}
	})

	t.Run("the agent reports the apps role", func(t *testing.T) {
		var role string
		if err := p.DB.QueryRow(t.Context(), "SELECT role FROM clusters WHERE id = $1", p.ClusterID).
			Scan(&role); err != nil {
			t.Fatal(err)
		}
		if role != "apps" {
			t.Errorf("cluster role %q, want apps", role)
		}
	})

	const imageApp, sourceApp = "e2e-public-image", "e2e-no-builds-source"
	t.Run("a public image deploys", func(t *testing.T) {
		dir := t.TempDir()
		platform.WriteLocoFile(t, dir, locofile.File{Partial: imageApp, Services: map[string]locofile.Service{
			imageApp: platform.ImageService(p.PublicImage, platform.Host(imageApp)),
		}})
		if out := w.MustLoco(t, dir, "deploy", "--yes").Stdout; !strings.Contains(out, "Started deployment") {
			t.Errorf("the CLI did not apply the file without a build:\n%s", out)
		}
		w.WaitReady(t, imageApp)
		var deployment appsv1.Deployment
		p.MustGet(t, &deployment, "-n", w.Namespace, "deployment", "resource-"+w.ResourceID(t, imageApp))
		repository := p.PublicImage[:strings.LastIndex(p.PublicImage, ":")]
		if image := deployment.Spec.Template.Spec.Containers[0].Image; !strings.HasPrefix(
			image,
			repository+"@sha256:",
		) {
			t.Errorf("the app runs %s, want the public image pinned by digest", image)
		}
	})

	source := t.TempDir()
	p.CopyFixture(t, "build-app", source)
	platform.WriteLocoFile(t, source, locofile.File{Partial: sourceApp, Services: map[string]locofile.Service{
		sourceApp: platform.SourceService(platform.Host(sourceApp)),
	}})

	t.Run("a source deploy explains builds are unavailable", func(t *testing.T) {
		result := w.Loco(t.Context(), source, nil, "deploy", "--yes")
		if result.Err == nil {
			t.Fatalf("loco deploy from source succeeded:\n%s", result.Output())
		}
		stderr := regexp.MustCompile(`\s+`).ReplaceAllString(result.Stderr, " ")
		for _, want := range []string{
			"cannot build from source: no cluster in this install accepts builds",
			"Set image on the service in loco.yaml",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("loco deploy stderr lacks %q:\n%s", want, result.Stderr)
			}
		}
		requireNoBuilds(t, w)
	})

	t.Run("--image skips the unavailable build", func(t *testing.T) {
		file := platform.ReadLocoFile(t, source)
		service := file.Services[sourceApp]
		service.Health.Path = "/"
		file.Services[sourceApp] = service
		platform.WriteLocoFile(t, source, file)
		before, err := os.ReadFile(filepath.Join(source, locofile.FileName))
		if err != nil {
			t.Fatal(err)
		}
		w.MustLoco(t, source, "deploy", sourceApp, "--image", p.PublicImage, "--yes")
		w.WaitReady(t, sourceApp)
		after, err := os.ReadFile(filepath.Join(source, locofile.FileName))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Error("--image changed loco.yaml")
		}
		requireNoBuilds(t, w)
	})

	t.Run("a workspace API token deploys without a session", func(t *testing.T) {
		const app, tokenName = "e2e-api-token", "e2e-cli-deploy" //nolint:gosec // names, not credentials
		dir := t.TempDir()
		platform.WriteLocoFile(
			t,
			dir,
			locofile.File{
				Partial:  app,
				Services: map[string]locofile.Service{app: platform.ImageService(p.PublicImage)},
			},
		)
		token := w.MintToken(t, tokenName, tokenv1.Scope_SCOPE_READ, tokenv1.Scope_SCOPE_WRITE)
		listed, err := p.User.Token.ListTokens(
			t.Context(),
			&tokenv1.ListTokensRequest{EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, EntityId: w.ID},
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, listedToken := range listed.GetTokens() {
			if listedToken.GetName() == tokenName && listedToken.GetCreatedBy() != p.UserID {
				t.Errorf("ListTokens reports creator %q, want %q", listedToken.GetCreatedBy(), p.UserID)
			}
		}
		found, err := p.User.Token.GetToken(
			t.Context(),
			&tokenv1.GetTokenRequest{
				Name:       tokenName,
				EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
				EntityId:   w.ID,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if found.GetToken().GetCreatedBy() != p.UserID {
			t.Errorf("GetToken reports creator %q, want %q", found.GetToken().GetCreatedBy(), p.UserID)
		}
		clean := &platform.Workspace{}
		*clean = *w
		clean.Home = t.TempDir()
		result := clean.Loco(t.Context(), dir, []string{"LOCO_TOKEN=" + token}, "deploy", "--yes",
			"--org", w.OrgID, "--workspace", w.ID, "--env", w.EnvironmentID)
		if result.Err != nil {
			t.Fatalf("a clean CLI deploy with a workspace write token: %v\n%s", result.Err, result.Output())
		}
		if !strings.Contains(result.Stdout, "Started deployment") {
			t.Errorf("the token did not authenticate an apply:\n%s", result.Stdout)
		}
		if _, err := os.Stat(filepath.Join(clean.Home, ".loco", "credentials.json")); err == nil {
			t.Error("the CLI wrote a session")
		}
		w.WaitReady(t, app)
	})
}

func requireNoBuilds(t *testing.T, w *platform.Workspace) {
	t.Helper()
	var count int
	const query = "SELECT count(*) FROM builds b JOIN resources r ON r.id = b.resource_id WHERE r.workspace_id = $1"
	if err := p.DB.QueryRow(t.Context(), query, w.ID).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("%d builds were created", count)
	}
}
