package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

const workspaceRuntimeLogTimeout = time.Minute

// Kubectl runs kubectl against the run's cluster and returns its standard output.
func (c *Cluster) Kubectl(ctx context.Context, args ...string) (string, error) {
	return c.Command(ctx, "kubectl", append([]string{"--context", c.kubeContext}, args...)...)
}

// KubectlInput runs kubectl with input on its standard input.
func (c *Cluster) KubectlInput(ctx context.Context, input string, args ...string) (string, error) {
	return c.commandStdin(ctx, []byte(input), "kubectl", append([]string{"--context", c.kubeContext}, args...)...)
}

// Apply applies a manifest to the run's cluster.
func (c *Cluster) Apply(ctx context.Context, manifest string) error {
	_, err := c.KubectlInput(ctx, manifest, "apply", "-f", "-")
	return err
}

// Replace replaces an object with a manifest.
func (c *Cluster) Replace(ctx context.Context, manifest string) error {
	_, err := c.KubectlInput(ctx, manifest, "replace", "-f", "-")
	return err
}

// Get decodes kubectl get's JSON for args into object.
func (c *Cluster) Get(ctx context.Context, object any, args ...string) error {
	out, err := c.Kubectl(ctx, append(append([]string{"get"}, args...), "-o", "json")...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(out), object); err != nil {
		return fmt.Errorf("decode kubectl get %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// Exists reports whether kubectl get finds the object args name.
func (c *Cluster) Exists(ctx context.Context, args ...string) (bool, error) {
	out, err := c.Kubectl(ctx, append(append([]string{"get"}, args...), "--ignore-not-found", "-o", "name")...)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// MustKubectl runs kubectl and fails the test when it fails.
func (c *Cluster) MustKubectl(tb testing.TB, args ...string) string {
	tb.Helper()
	out, err := c.Kubectl(tb.Context(), args...)
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// MustGet decodes kubectl get's JSON for args into object and fails the test when it fails.
func (c *Cluster) MustGet(tb testing.TB, object any, args ...string) {
	tb.Helper()
	if err := c.Get(tb.Context(), object, args...); err != nil {
		tb.Fatal(err)
	}
}

// MustApply applies a manifest and fails the test when it fails.
func (c *Cluster) MustApply(tb testing.TB, manifest string) {
	tb.Helper()
	if err := c.Apply(tb.Context(), manifest); err != nil {
		tb.Fatal(err)
	}
}

// WorkspaceNamespace is the namespace of a workspace's workloads.
func (p *Platform) WorkspaceNamespace(workspaceID string) string {
	return p.values.workspacePrefix + workspaceID
}

// Node is the name of the run's one kind node.
func (c *Cluster) Node() string {
	return c.cluster + "-control-plane"
}

// DeleteOnCleanup deletes the object args name once the test ends, without waiting.
func (c *Cluster) DeleteOnCleanup(tb testing.TB, args ...string) {
	tb.Helper()
	tb.Cleanup(func() {
		command := append(append([]string{"delete"}, args...), "--ignore-not-found", "--wait=false")
		if _, err := c.Kubectl(context.WithoutCancel(tb.Context()), command...); err != nil {
			tb.Log(err)
		}
	})
}

// LogKubectl logs what each kubectl command line prints, for a failed test.
func (c *Cluster) LogKubectl(tb testing.TB, commands ...string) {
	tb.Helper()
	for _, command := range commands {
		out, err := c.Kubectl(context.WithoutCancel(tb.Context()), strings.Fields(command)...)
		tb.Logf("kubectl %s:\n%s%v", command, out, err)
	}
}

// LogWorkspaceRuntime captures workload failures before cleanup removes the resources.
func (p *Platform) LogWorkspaceRuntime(tb testing.TB, namespace string) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(tb.Context()), workspaceRuntimeLogTimeout)
	defer cancel()
	for _, args := range [][]string{
		{"-n", namespace, "get", "pods",
			"-o=custom-columns=NAME:.metadata.name,IP:.status.podIP,NODE:.spec.nodeName,PHASE:.status.phase," +
				"REASON:.status.containerStatuses[*].state.waiting.reason," +
				"MESSAGE:.status.containerStatuses[*].state.waiting.message"},
		{"-n", namespace, "get", "events", "--sort-by=.metadata.creationTimestamp"},
		{"-n", namespace, "get", "secret", "loco-registry-pull",
			"-o=custom-columns=NAME:.metadata.name,UID:.metadata.uid,CREATED:.metadata.creationTimestamp," +
				"DELETING:.metadata.deletionTimestamp,TYPE:.type,RESOURCEVERSION:.metadata.resourceVersion"},
	} {
		out, err := p.Kubectl(ctx, args...)
		tb.Logf("kubectl %s:\n%s%v", strings.Join(args, " "), out, err)
	}
	out, err := p.Command(ctx, "docker", "exec", p.Node(), "journalctl", "-u", "kubelet",
		"--since=-5min", "--no-pager", "--output=short-iso-precise", "--lines=200", "--grep="+namespace)
	tb.Logf("workspace kubelet messages:\n%s%v", out, err)
}
