package apps

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const yamlSecretTimeout = 2 * time.Minute

var (
	errSecretStillDelivered = errors.New("the workload still receives the secret")
	errNoPods               = errors.New("the workload has no pods")
)

// TestASecretRemovedFromLocoYamlStopsReachingTheWorkload checks a deploy without a secret the
// file declared before leaves it out of the workload and keeps its stored value.
func TestASecretRemovedFromLocoYamlStopsReachingTheWorkload(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const (
		app   = "e2e-yaml-secret"
		name  = "STRIPE_API_KEY"
		value = "e2e-stripe-value"
	)
	dir := t.TempDir()
	service := platform.ImageService(p.PublicImage)
	service.Secrets = []string{name}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	platform.WriteLocoFile(t, dir, file)

	if result := w.Loco(t.Context(), dir, nil, "deploy", "--env", platform.Production, "--yes"); result.Err == nil {
		t.Fatalf("the first deploy did not stop for the missing secret:\n%s", result.Output())
	}
	w.MustLoco(t, dir, "env", "set", "--service", app, "--env", platform.Production, name+"="+value)
	w.MustLoco(t, dir, "deploy", "--env", platform.Production, "--yes", "--wait")
	waitWorkloadEnv(t, w, app, func(env string) error {
		if !strings.Contains(env, name+"="+value+"\n") {
			return fmt.Errorf("the workload environment lacks %s:\n%s", name, env)
		}
		return nil
	})

	service.Secrets = nil
	file.Services[app] = service
	platform.WriteLocoFile(t, dir, file)
	out := w.MustLoco(t, dir, "deploy", "--env", platform.Production, "--yes", "--wait").Stdout
	hint := "  " + name + ": Saved. To use this key, add " + name + " to services." + app + ".secrets and redeploy.\n"
	if !strings.Contains(out, hint) {
		t.Errorf("the deploy does not say how to use the saved %s:\n%s", name, out)
	}
	waitWorkloadEnv(t, w, app, func(env string) error {
		if strings.Contains(env, name+"=") {
			return errSecretStillDelivered
		}
		return nil
	})
	listed := w.MustLoco(t, dir, "env", "list", "--service", app, "--env", platform.Production).Stdout
	if !strings.Contains(listed, name) {
		t.Fatalf("%s is no longer stored:\n%s", name, listed)
	}
}

// waitWorkloadEnv waits until every pod of the app's production workload has an environment check accepts.
func waitWorkloadEnv(t *testing.T, w *platform.Workspace, app string, check func(env string) error) {
	t.Helper()
	platform.Eventually(t, yamlSecretTimeout, "the workload environment of "+app, func(ctx context.Context) error {
		resource, err := w.Resource(ctx, app)
		if err != nil {
			return err
		}
		pods, err := p.Kubectl(ctx, "-n", w.Namespace, "get", "pods",
			"-l", "app=resource-"+resource.GetId(), "-o", "name")
		if err != nil {
			return err
		}
		if strings.TrimSpace(pods) == "" {
			return errNoPods
		}
		for pod := range strings.FieldsSeq(pods) {
			env, err := p.Kubectl(ctx, "-n", w.Namespace, "exec", pod, "--", "env")
			if err != nil {
				return err
			}
			if err := check(env); err != nil {
				return fmt.Errorf("%s: %w", pod, err)
			}
		}
		return nil
	})
}
