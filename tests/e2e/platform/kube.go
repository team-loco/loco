package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Kubectl runs kubectl against the run's cluster and returns its standard output.
func (p *Platform) Kubectl(ctx context.Context, args ...string) (string, error) {
	return p.Command(ctx, "kubectl", append([]string{"--context", p.kubeContext}, args...)...)
}

// Apply applies a manifest to the run's cluster.
func (p *Platform) Apply(ctx context.Context, manifest string) error {
	_, err := p.commandStdin(ctx, []byte(manifest), "kubectl", "--context", p.kubeContext, "apply", "-f", "-")
	return err
}

// Replace replaces an object with a manifest.
func (p *Platform) Replace(ctx context.Context, manifest string) error {
	_, err := p.commandStdin(ctx, []byte(manifest), "kubectl", "--context", p.kubeContext, "replace", "-f", "-")
	return err
}

// Get decodes kubectl get's JSON for args into object.
func (p *Platform) Get(ctx context.Context, object any, args ...string) error {
	out, err := p.Kubectl(ctx, append(append([]string{"get"}, args...), "-o", "json")...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(out), object); err != nil {
		return fmt.Errorf("decode kubectl get %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// Exists reports whether kubectl get finds the object args name.
func (p *Platform) Exists(ctx context.Context, args ...string) (bool, error) {
	out, err := p.Kubectl(ctx, append(append([]string{"get"}, args...), "--ignore-not-found", "-o", "name")...)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// MustKubectl runs kubectl and fails the test when it fails.
func (p *Platform) MustKubectl(tb testing.TB, args ...string) string {
	tb.Helper()
	out, err := p.Kubectl(tb.Context(), args...)
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// MustGet decodes kubectl get's JSON for args into object and fails the test when it fails.
func (p *Platform) MustGet(tb testing.TB, object any, args ...string) {
	tb.Helper()
	if err := p.Get(tb.Context(), object, args...); err != nil {
		tb.Fatal(err)
	}
}

// MustApply applies a manifest and fails the test when it fails.
func (p *Platform) MustApply(tb testing.TB, manifest string) {
	tb.Helper()
	if err := p.Apply(tb.Context(), manifest); err != nil {
		tb.Fatal(err)
	}
}

// WorkspaceNamespace is the namespace of a workspace's workloads.
func (p *Platform) WorkspaceNamespace(workspaceID string) string {
	return p.values.workspacePrefix + workspaceID
}

// Node is the name of the run's one kind node.
func (p *Platform) Node() string {
	return p.cluster + "-control-plane"
}

// DeleteOnCleanup deletes the object args name once the test ends, without waiting.
func (p *Platform) DeleteOnCleanup(tb testing.TB, args ...string) {
	tb.Helper()
	tb.Cleanup(func() {
		command := append(append([]string{"delete"}, args...), "--ignore-not-found", "--wait=false")
		if _, err := p.Kubectl(context.WithoutCancel(tb.Context()), command...); err != nil {
			tb.Log(err)
		}
	})
}

// LogKubectl logs what each kubectl command line prints, for a failed test.
func (p *Platform) LogKubectl(tb testing.TB, commands ...string) {
	tb.Helper()
	for _, command := range commands {
		out, err := p.Kubectl(context.WithoutCancel(tb.Context()), strings.Fields(command)...)
		tb.Logf("kubectl %s:\n%s%v", command, out, err)
	}
}
