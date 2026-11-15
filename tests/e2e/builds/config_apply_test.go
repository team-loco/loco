package builds

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"sigs.k8s.io/yaml"

	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	configWeb   = "e2e-config-web"
	configCache = "e2e-config-cache"
	hpaTimeout  = 2 * time.Minute
)

// TestConfigApply drives loco.yaml through validate, plan, apply and deploy: environment
// overrides, a clean second plan, optional health checks, a replica change, autoscaling, a
// destructive delete and a disabled environment.
func TestConfigApply(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	repo := t.TempDir()
	p.CopyFixture(t, "build-app", filepath.Join(repo, "services", "web"))
	web := platform.SourceService(platform.Host(configWeb))
	web.Context = "services/web"
	web.Env = map[string]string{"TIER": "base"}
	web.Environments = map[string]locofile.Override{
		platform.Production: {Env: map[string]string{"TIER": platform.Production}},
	}
	cache := platform.Service(8080)
	cache.Image = p.PublicImage
	file := locofile.File{
		Partial:  "e2e-config",
		Services: map[string]locofile.Service{configWeb: web, configCache: cache},
	}
	platform.WriteLocoFile(t, repo, file)
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=Loco test", "-c", "user.email=test@loco.test", "commit", "-qm", "Initial app"},
	} {
		if _, err := p.Command(t.Context(), "git", append([]string{"-C", repo}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	loco := func(t *testing.T, args ...string) string {
		t.Helper()
		return w.MustLoco(t, repo, args...).Stdout
	}
	plan := func(t *testing.T) string {
		t.Helper()
		return loco(t, "infra", "plan", "--env", platform.Production)
	}
	apply := func(t *testing.T, args ...string) string {
		t.Helper()
		return loco(t, append([]string{"infra", "apply", "--env", platform.Production, "--yes"}, args...)...)
	}
	edit := func(t *testing.T, change func(*locofile.File)) {
		t.Helper()
		change(&file)
		platform.WriteLocoFile(t, repo, file)
	}

	t.Run("validate accepts the file", func(t *testing.T) {
		requireContains(t, loco(t, "infra", "validate"), configCache+", "+configWeb)
	})

	t.Run("plan creates both services", func(t *testing.T) {
		requireContains(t, plan(t), "Plan: 2 to create", "needs deploy", "env.TIER: "+platform.Production)
		if resourceCount(t, w, configWeb) != 0 {
			t.Error("plan created a resource")
		}
	})

	t.Run("apply creates the image service", func(t *testing.T) {
		out := apply(t)
		if resourceCount(t, w, configCache) != 1 {
			t.Errorf("the image service has no resource")
		}
		requireMatch(t, out, "Started deployment .* for "+configCache)
		requireDeploymentOrigin(t, w, repo, configCache, false)
		w.WaitReady(t, configCache)
		requireContains(t, plan(t), "needs deploy")
	})

	t.Run("deploy builds and applies", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(repo, "uncommitted.txt"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		out := loco(t, "deploy", "--env", platform.Production, "--yes")
		t.Logf("loco deploy took %s", time.Since(started))
		requireDeploymentOrigin(t, w, repo, configWeb, true)
		requireContains(t, out, "Building "+configWeb+" from deploy/Dockerfile in services/web", "succeeded in",
			configWeb+": https://"+platform.Host(configWeb))
		w.WaitReady(t, configWeb)
		w.WaitReady(t, configCache)
	})

	t.Run("a second plan is clean", func(t *testing.T) {
		requireContains(t, plan(t), "No changes")
	})

	t.Run("a second plan by name is clean", func(t *testing.T) {
		config := w.ConfigPath()
		if err := os.Rename(config, config+".saved"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Rename(config+".saved", config); err != nil {
				t.Error(err)
			}
		}()
		out := loco(t, "infra", "plan", "--env", platform.Production, "--org", w.OrgName, "--workspace", w.Name)
		requireContains(t, out, "No changes")
	})

	t.Run("health checks are optional", func(t *testing.T) {
		requireProbes(t, w, configWeb, true)
		requireProbes(t, w, configCache, false)
		removeOverrideHealth(t, repo, configWeb)
		apply(t)
		platform.Eventually(
			t,
			2*time.Minute,
			"the HTTP probes to give way to the TCP readiness probe",
			func(ctx context.Context) error {
				return probesMatch(ctx, w, configWeb, false)
			},
		)
		requireContains(t, plan(t), "No changes")
		// The typed file cannot hold an explicit null; without health anywhere it plans the same.
		edit(t, func(f *locofile.File) {
			service := f.Services[configWeb]
			service.Health = nil
			f.Services[configWeb] = service
		})
		requireContains(t, plan(t), "No changes")
	})

	t.Run("a replica change is one update", func(t *testing.T) {
		edit(t, func(f *locofile.File) {
			setRegion(f, configCache, func(r *locofile.Region) { r.Replicas.Max = new(int32(2)) })
		})
		requireContains(t, plan(t), "Plan: 1 to update", "replicas")
		apply(t)
		requireContains(t, plan(t), "No changes")
	})

	t.Run("scaling targets reach the worker", func(t *testing.T) {
		waitCacheRunning(t, w)
		for _, target := range []int32{70, 60} {
			edit(t, func(f *locofile.File) {
				setRegion(
					f,
					configCache,
					func(r *locofile.Region) { r.Autoscaling = &locofile.Autoscaling{CPUTarget: new(target)} },
				)
			})
			apply(t)
			platform.Eventually(
				t,
				hpaTimeout,
				fmt.Sprintf("the HPA to read metrics with target %d", target),
				func(ctx context.Context) error {
					return autoscalerMatches(ctx, w, target)
				},
			)
			waitCacheRunning(t, w)
		}
		edit(t, func(f *locofile.File) { setRegion(f, configCache, func(r *locofile.Region) { r.Autoscaling = nil }) })
		apply(t)
		id := w.ResourceID(t, configCache)
		platform.Eventually(t, time.Minute, "the controller to remove the HPA", func(ctx context.Context) error {
			if found, err := p.Exists(ctx, "-n", w.Namespace, "hpa", "resource-"+id); err != nil || found {
				return fmt.Errorf("the HPA exists (%w)", err)
			}
			return nil
		})
		waitCacheRunning(t, w)
	})

	t.Run("removing a service deletes it", func(t *testing.T) {
		id := w.ResourceID(t, configCache)
		edit(t, func(f *locofile.File) { delete(f.Services, configCache) })
		requireContains(t, plan(t), "Plan: 1 to delete", "destructive")
		if result := w.Loco(
			t.Context(),
			repo,
			nil,
			"infra",
			"apply",
			"--env",
			platform.Production,
			"--yes",
		); result.Err == nil {
			t.Errorf("apply deleted a service without --confirm-destructive:\n%s", result.Output())
		}
		if resourceCount(t, w, configCache) != 1 {
			t.Error("the refused apply removed the service")
		}
		apply(t, "--confirm-destructive")
		if resourceCount(t, w, configCache) != 0 {
			t.Error("the deleted service's resource remains")
		}
		waitApplicationGone(t, id)
	})

	t.Run("disabling the environment stops the service", func(t *testing.T) {
		edit(t, func(f *locofile.File) {
			service := f.Services[configWeb]
			override := service.Environments[platform.Production]
			override.Enabled = new(false)
			service.Environments[platform.Production] = override
			f.Services[configWeb] = service
		})
		requireContains(t, plan(t), "Plan: 1 to update", "enabled: true -> false")
		apply(t, "--confirm-destructive")
		id := w.ResourceID(t, configWeb)
		if resourceCount(t, w, configWeb) != 1 {
			t.Error("disabling the environment deleted the resource")
		}
		if placements(t, id, "AND NOT desired_deleted") != 0 {
			t.Error("apply did not mark the placement deleted")
		}
		platform.Eventually(t, time.Minute, "the agent to delete the placement", func(ctx context.Context) error {
			var count int
			if err := p.DB.QueryRow(ctx, "SELECT count(*) FROM placements WHERE resource_id = $1", id).
				Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("%d placements", count)
			}
			return nil
		})
		waitApplicationGone(t, id)
		requireContains(t, plan(t), "No changes")
	})
}

func requireContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func resourceCount(t *testing.T, w *platform.Workspace, name string) int {
	t.Helper()
	var count int
	const query = "SELECT count(*) FROM resources WHERE workspace_id = $1 AND name = $2"
	if err := p.DB.QueryRow(t.Context(), query, w.ID, name).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func placements(t *testing.T, resourceID, condition string) int {
	t.Helper()
	var count int
	if err := p.DB.QueryRow(t.Context(), "SELECT count(*) FROM placements WHERE resource_id = $1 "+condition, resourceID).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// requireDeploymentOrigin checks the active deployment records the CLI user and the repository's
// HEAD, with dirty set when the work tree has uncommitted changes.
func requireDeploymentOrigin(t *testing.T, w *platform.Workspace, repo, name string, dirty bool) {
	t.Helper()
	head, err := p.Command(t.Context(), "git", "-C", repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := p.DB.QueryRow(t.Context(), `SELECT count(*) FROM deployments d JOIN resources r ON r.id = d.resource_id
		WHERE r.workspace_id = $1 AND r.name = $2 AND d.is_active AND d.actor_type = 'user' AND d.actor_id = $3
		  AND d.method = 'cli' AND d.commit_sha = $4 AND d.commit_dirty = $5`,
		w.ID, name, p.UserID, strings.TrimSpace(head), dirty).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("%s has %d active CLI deployments by the user at HEAD with dirty=%v, want 1", name, count, dirty)
	}
}

func requireMatch(t *testing.T, out, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(out) {
		t.Errorf("output does not match %q:\n%s", pattern, out)
	}
}

func requireProbes(t *testing.T, w *platform.Workspace, name string, http bool) {
	t.Helper()
	if err := probesMatch(t.Context(), w, name, http); err != nil {
		t.Error(err)
	}
}

// probesMatch checks the service's container probes: liveness and HTTP readiness probes with a
// health check, and only a TCP readiness probe on the port without one.
func probesMatch(ctx context.Context, w *platform.Workspace, name string, http bool) error {
	resource, err := w.Resource(ctx, name)
	if err != nil {
		return err
	}
	var deployment appsv1.Deployment
	if err := p.Get(ctx, &deployment, "-n", w.Namespace, "deployment", "resource-"+resource.GetId()); err != nil {
		return err
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	readiness := container.ReadinessProbe
	if (container.LivenessProbe != nil) != http || readiness == nil || (readiness.HTTPGet != nil) != http ||
		(readiness.TCPSocket != nil) == http {
		return fmt.Errorf(
			"%s probes liveness=%v readiness=%+v, want HTTP probes %v",
			name,
			container.LivenessProbe != nil,
			readiness,
			http,
		)
	}
	return nil
}

// removeOverrideHealth sets the service's production health to null, which removes the health
// check the environment would inherit.
func removeOverrideHealth(t *testing.T, repo, service string) {
	t.Helper()
	path := filepath.Join(repo, locofile.FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err = yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	override, ok := lookup(raw, "services", service, "environments", platform.Production)
	if !ok {
		t.Fatalf("loco.yaml has no %s override for %s", platform.Production, service)
	}
	override["health"] = nil
	if data, err = yaml.Marshal(raw); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func setRegion(f *locofile.File, service string, change func(*locofile.Region)) {
	s := f.Services[service]
	region := s.Regions[p.Region]
	change(&region)
	s.Regions[p.Region] = region
	f.Services[service] = s
}

func waitCacheRunning(t *testing.T, w *platform.Workspace) {
	t.Helper()
	platform.Eventually(t, time.Minute, "the cache's active deployment to run", func(ctx context.Context) error {
		var count int
		if err := p.DB.QueryRow(ctx, `SELECT count(*) FROM deployments d JOIN resources r ON r.id = d.resource_id
			WHERE r.workspace_id = $1 AND r.name = $2 AND d.is_active AND d.status = 'running'`,
			w.ID, configCache).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("%d running active deployments", count)
		}
		return nil
	})
}

func autoscalerMatches(ctx context.Context, w *platform.Workspace, target int32) error {
	resource, err := w.Resource(ctx, configCache)
	if err != nil {
		return err
	}
	var hpa autoscalingv2.HorizontalPodAutoscaler
	if err := p.Get(ctx, &hpa, "-n", w.Namespace, "hpa", "resource-"+resource.GetId()); err != nil {
		return err
	}
	spec := hpa.Spec
	if spec.MinReplicas == nil || *spec.MinReplicas != 1 || spec.MaxReplicas != 2 ||
		spec.ScaleTargetRef.Name != "resource-"+resource.GetId() ||
		len(spec.Metrics) == 0 || spec.Metrics[0].Resource == nil ||
		spec.Metrics[0].Resource.Target.AverageUtilization == nil ||
		*spec.Metrics[0].Resource.Target.AverageUtilization != target ||
		len(hpa.OwnerReferences) == 0 ||
		hpa.OwnerReferences[0].Kind != "Deployment" {
		return fmt.Errorf("HPA spec %+v owned by %+v", spec, hpa.OwnerReferences)
	}
	for _, condition := range hpa.Status.Conditions {
		if condition.Type == autoscalingv2.ScalingActive && condition.Status == "True" {
			return nil
		}
	}
	return errors.New("the HPA is not ScalingActive")
}

func waitApplicationGone(t *testing.T, resourceID string) {
	t.Helper()
	platform.Eventually(t, time.Minute, "the Application to be removed", func(ctx context.Context) error {
		if found, err := p.Exists(ctx, "-n", p.Namespace, "application", "resource-"+resourceID); err != nil || found {
			return fmt.Errorf("the Application exists (%w)", err)
		}
		return nil
	})
}

// lookup follows keys through nested YAML mappings.
func lookup(node map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		next, ok := node[key].(map[string]any)
		if !ok {
			return nil, false
		}
		node = next
	}
	return node, true
}
