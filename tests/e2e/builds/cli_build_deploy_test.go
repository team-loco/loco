package builds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const secretTimeout = 2 * time.Minute

// TestCLIBuildDeploy deploys from source with the loco CLI: the build, its commit, its logs and
// listing, a deploy without loco.yaml, and secrets set, delivered and rotated through Transit.
func TestCLIBuildDeploy(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-cli-app"
	dir := t.TempDir()
	p.CopyFixture(t, "build-app", dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=never-uploaded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	platform.WriteLocoFile(
		t,
		dir,
		locofile.File{
			Partial:  app,
			Services: map[string]locofile.Service{app: platform.SourceService(platform.Host(app))},
		},
	)
	git := func(args ...string) string {
		out, err := p.Command(
			t.Context(),
			"git",
			append([]string{"-C", dir, "-c", "user.name=e2e", "-c", "user.email=e2e@loco.test"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	git("init", "-q", "-b", "e2e-branch")
	git("add", "-A")
	git("commit", "-q", "-m", "e2e source commit")

	t.Run("deploys from source", func(t *testing.T) {
		started := time.Now()
		deployed := w.MustLoco(t, dir, "deploy", "--yes", "--wait")
		t.Logf("loco deploy took %s", time.Since(started))
		url := app + ": https://" + platform.Host(app)
		for _, want := range []string{"Packed", "succeeded in", "Started deployment", url} {
			if !strings.Contains(deployed.Stdout, want) {
				t.Errorf("loco deploy output lacks %q:\n%s", want, deployed.Stdout)
			}
		}
		w.MustLoco(t, dir, "builds", "logs", listBuilds(t, w, dir, app)[0].ID)
	})

	t.Run("lists the build with its commit", func(t *testing.T) {
		if out := w.MustLoco(t, dir, "builds", "list", app).Stdout; !strings.Contains(out, "succeeded") {
			t.Errorf("loco builds list does not show the build succeeded:\n%s", out)
		}
		if out := w.MustLoco(
			t,
			dir,
			"builds",
			"list",
			app,
			"--output",
			"json",
		).Stdout; !strings.Contains(
			out,
			`"imageDigest":"sha256:`,
		) {
			t.Errorf("loco builds list --output json reports no digest:\n%s", out)
		}
		build := listBuilds(t, w, dir, app)[0]
		got := build.GitCommit + " " + build.GitBranch + " " + build.GitCommitMessage
		if want := git("rev-parse", "HEAD") + " e2e-branch e2e source commit"; got != want {
			t.Errorf("the build records commit %q, want %q", got, want)
		}
	})

	t.Run("the deployment runs the build", func(t *testing.T) {
		build := listBuilds(t, w, dir, app)[0]
		image := build.ImageRepository + "@" + build.ImageDigest
		if !regexp.MustCompile(`@sha256:[a-f0-9]{64}$`).MatchString(image) {
			t.Fatalf("loco builds list reported image %q", image)
		}
		var deployment appsv1.Deployment
		p.MustGet(t, &deployment, "-n", w.Namespace, "deployment", "resource-"+w.ResourceID(t, app))
		if got := deployment.Spec.Template.Spec.Containers[0].Image; got != image {
			t.Errorf("the app runs %s, want the CLI's build %s", got, image)
		}
	})

	t.Run("deploys without loco.yaml", func(t *testing.T) {
		const minimal = "e2e-cli-minimal"
		minimalDir := t.TempDir()
		p.CopyFixture(t, "build-app", minimalDir)
		out := w.MustLoco(t, minimalDir, "deploy", "--name", minimal, "--port", "8080", "--region", p.Region,
			"--dockerfile", "deploy/Dockerfile", "--yes", "--wait").Stdout
		if _, err := os.Stat(filepath.Join(minimalDir, locofile.FileName)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a deploy without loco.yaml wrote one (%v)", err)
		}
		url := regexp.MustCompile(
			`(?m)^` + minimal + `: https://` + minimal + `-[a-z]{4}\.` + regexp.QuoteMeta(p.Domain) + `$`,
		)
		if !url.MatchString(out) {
			t.Errorf("loco deploy reports no suffixed public URL:\n%s", out)
		}
	})

	const secretApp = "e2e-cli-secret" //nolint:gosec // an app name
	secretDir := t.TempDir()
	service := platform.ImageService(p.PublicImage)
	service.Secrets = []string{"E2E_SECRET"}
	platform.WriteLocoFile(
		t,
		secretDir,
		locofile.File{Partial: secretApp, Services: map[string]locofile.Service{secretApp: service}},
	)

	t.Run("deploys a secret", func(t *testing.T) {
		result := w.Loco(t.Context(), secretDir, nil, "deploy", "--env", platform.Production, "--yes")
		if result.Err == nil ||
			!strings.Contains(result.Output(), "Created "+secretApp+". Set its secrets with: loco env set") {
			t.Fatalf("the first deploy did not ask for the missing secret (%v):\n%s", result.Err, result.Output())
		}
		w.MustLoco(
			t,
			secretDir,
			"env",
			"set",
			"--service",
			secretApp,
			"--env",
			platform.Production,
			"E2E_SECRET=e2e-secret-initial",
		)
		w.MustLoco(t, secretDir, "deploy", "--env", platform.Production, "--yes")
		waitPodSecret(t, w, secretApp, "e2e-secret-initial")
	})

	t.Run("rotates a secret", func(t *testing.T) {
		w.MustLoco(
			t,
			secretDir,
			"env",
			"set",
			"--service",
			secretApp,
			"--env",
			platform.Production,
			"E2E_SECRET=e2e-secret-rotated",
		)
		waitPodSecret(t, w, secretApp, "e2e-secret-rotated")
		w.MustLoco(t, secretDir, "deploy", "--env", platform.Production, "--yes")
		waitPodSecret(t, w, secretApp, "e2e-secret-rotated")
	})
}

type listedBuild struct {
	ID               string `json:"id"`
	ImageRepository  string `json:"imageRepository"`
	ImageDigest      string `json:"imageDigest"`
	GitCommit        string `json:"gitCommit"`
	GitBranch        string `json:"gitBranch"`
	GitCommitMessage string `json:"gitCommitMessage"`
}

func listBuilds(t *testing.T, w *platform.Workspace, dir, app string) []listedBuild {
	t.Helper()
	var listed struct {
		Builds []listedBuild `json:"builds"`
	}
	if err := json.Unmarshal(
		[]byte(w.MustLoco(t, dir, "builds", "list", app, "--output", "json").Stdout),
		&listed,
	); err != nil {
		t.Fatal(err)
	}
	if len(listed.Builds) == 0 {
		t.Fatalf("loco builds list shows no build of %s", app)
	}
	return listed.Builds
}

// waitPodSecret waits until the app's production deployment runs and its pod holds want.
func waitPodSecret(t *testing.T, w *platform.Workspace, app, want string) {
	t.Helper()
	platform.Eventually(
		t,
		secretTimeout,
		"the pod to receive "+want+" through Transit",
		func(ctx context.Context) error {
			resource, err := w.Resource(ctx, app)
			if err != nil {
				return err
			}
			// The pod can start before the agent reports rollout completion to the API.
			for _, environment := range resource.GetEnvironmentStatuses() {
				for _, region := range environment.GetRegions() {
					if environment.GetEnvironmentName() == platform.Production &&
						region.GetDeploymentStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING {
						return fmt.Errorf("deployment %v", region.GetDeploymentStatus())
					}
				}
			}
			value, err := p.Kubectl(
				ctx,
				"-n",
				w.Namespace,
				"exec",
				"deployment/resource-"+resource.GetId(),
				"--",
				"printenv",
				"E2E_SECRET",
			)
			if err != nil {
				return err
			}
			if got := strings.TrimSpace(value); got != want {
				return fmt.Errorf("E2E_SECRET is %q", got)
			}
			return nil
		},
	)
}
