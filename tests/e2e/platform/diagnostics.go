package platform

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	diagnosticTimeout = 30 * time.Second
	diagnosticBudget  = 2 * time.Minute
	diagnosticDocker  = "docker"
	diagnosticContext = "--context"
)

func (c *Cluster) dumpCluster(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), diagnosticBudget)
	defer cancel()
	dump := func(command ...string) string {
		out, err := c.diagnostic(ctx, command...)
		logf("%s:\n%s%v", strings.Join(command, " "), tail(out), err)
		if err != nil {
			return ""
		}
		return out
	}
	for _, args := range [][]string{
		{"inspect", "--format", "status={{.State.Status}} exit={{.State.ExitCode}} running={{.State.Running}}", c.Node()},
		{"stats", "--no-stream", "--format", "cpu={{.CPUPerc}} memory={{.MemUsage}} pids={{.PIDs}}", c.Node()},
		{"logs", "--tail", "200", c.Node()},
		{"exec", c.Node(), "journalctl", "-u", "kubelet", "--no-pager", "--lines=200"},
	} {
		dump(append([]string{diagnosticDocker}, args...)...)
	}
	for _, component := range []string{"etcd", "kube-apiserver", "kube-scheduler", "kube-controller-manager"} {
		ids := dump(diagnosticDocker, "exec", c.Node(), "crictl", "ps", "-a", "--name", component, "-q")
		for id := range strings.FieldsSeq(ids) {
			dump(diagnosticDocker, "exec", c.Node(), "crictl", "logs", "--tail=200", id)
		}
	}
	for _, command := range []string{
		"get --raw=/readyz?verbose",
		"get nodes -o wide",
		"-n kube-system get pods -o wide",
		"-n kube-system get leases",
		"get events -A --sort-by=.metadata.creationTimestamp",
	} {
		dump(append([]string{"kubectl", diagnosticContext, c.kubeContext}, strings.Fields(command)...)...)
	}
}

func (c *Cluster) diagnostic(ctx context.Context, command ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, diagnosticTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = c.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// LogNamespace captures pod states and events without pod specifications or environment values.
func (c *Cluster) LogNamespace(tb testing.TB, namespace string) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(tb.Context()), diagnosticBudget)
	defer cancel()
	for _, command := range []string{
		"get pods -o=custom-columns=NAME:.metadata.name,PHASE:.status.phase," +
			"CONTAINERS:.status.containerStatuses[*].name,READY:.status.containerStatuses[*].ready," +
			"RESTARTS:.status.containerStatuses[*].restartCount,WAITING:.status.containerStatuses[*].state.waiting.reason," +
			"TERMINATED:.status.containerStatuses[*].state.terminated.reason," +
			"EXIT:.status.containerStatuses[*].state.terminated.exitCode",
		"get events --sort-by=.metadata.creationTimestamp",
	} {
		args := append(
			[]string{"kubectl", diagnosticContext, c.kubeContext, "-n", namespace},
			strings.Fields(command)...)
		out, err := c.diagnostic(ctx, args...)
		tb.Logf("%s:\n%s%v", strings.Join(args, " "), tail(out), err)
	}
}
