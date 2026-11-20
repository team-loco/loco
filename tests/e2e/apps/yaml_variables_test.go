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

// TestALocoYamlVariableReadsASecretOfTheEnvironment checks a variable that refers to a secret
// reaches the workload with the secret's value, whole and inside a longer value, and that a deploy
// without the reference leaves the value out while the secret stays stored.
func TestALocoYamlVariableReadsASecretOfTheEnvironment(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const (
		app   = "e2e-yaml-secret"
		name  = "STRIPE_API_KEY"
		value = "e2e-stripe-value"
	)
	dir := t.TempDir()
	service := platform.ImageService(p.PublicImage)
	service.Env = map[string]string{
		name:        "${{ secrets." + name + " }}",
		"KEY_URL":   "stripe://${{ secrets." + name + " }}@api",
		"SELF_HOST": "${{ " + app + ".PRIVATE_HOST }}",
	}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	platform.WriteLocoFile(t, dir, file)

	if result := w.Loco(t.Context(), dir, nil, "deploy", "--env", platform.Production, "--yes"); result.Err == nil {
		t.Fatalf("the first deploy did not stop for the missing secret:\n%s", result.Output())
	}
	w.MustLoco(t, dir, "secret", "set", "--env", platform.Production, name+"="+value)
	w.MustLoco(t, dir, "deploy", "--env", platform.Production, "--yes", "--wait")
	waitWorkloadEnv(t, w, app, func(env string) error {
		for _, want := range []string{name + "=" + value, "KEY_URL=stripe://" + value + "@api", "SELF_HOST=" + app} {
			if !strings.Contains(env, want+"\n") {
				return fmt.Errorf("the workload environment lacks %s:\n%s", want, env)
			}
		}
		return nil
	})

	service.Env = nil
	file.Services[app] = service
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--env", platform.Production, "--yes", "--wait")
	waitWorkloadEnv(t, w, app, func(env string) error {
		if strings.Contains(env, value) {
			return errSecretStillDelivered
		}
		return nil
	})
	listed := w.MustLoco(t, dir, "secret", "list", "--env", platform.Production).Stdout
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
