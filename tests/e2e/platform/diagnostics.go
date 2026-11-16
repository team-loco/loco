package platform

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	diagnosticKubectl   = "kubectl"
	diagnosticCRICTL    = "crictl"
	diagnosticEvents    = "events"
	diagnosticTimeout   = 30 * time.Second
	diagnosticBudget    = 2 * time.Minute
	diagnosticDocker    = "docker"
	diagnosticExec      = "exec"
	diagnosticContext   = "--context"
	diagnosticGet       = "get"
	diagnosticPods      = "pods"
	diagnosticEventSort = "--sort-by=.metadata.creationTimestamp"
)

func (c *Cluster) dumpCluster(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), diagnosticBudget)
	defer cancel()
	commands := [][]string{
		{
			diagnosticDocker,
			"inspect",
			"--format",
			"status={{.State.Status}} exit={{.State.ExitCode}} running={{.State.Running}}",
			c.Node(),
		},
		{diagnosticDocker, "logs", "--tail", "200", c.Node()},
		{diagnosticDocker, diagnosticExec, c.Node(), "journalctl", "-u", "kubelet", "--no-pager", "--lines=200"},
		{diagnosticKubectl, diagnosticContext, c.kubeContext, diagnosticGet, "nodes", "-o", "wide"},
		{
			diagnosticKubectl,
			diagnosticContext,
			c.kubeContext,
			"-n",
			"kube-system",
			diagnosticGet,
			diagnosticPods,
			"-o",
			"wide",
		},
		{
			diagnosticKubectl,
			diagnosticContext,
			c.kubeContext,
			diagnosticGet,
			diagnosticEvents,
			"-A",
			diagnosticEventSort,
		},
		{diagnosticDocker, diagnosticExec, c.Node(), diagnosticCRICTL, "ps", "-a", "--name", "etcd", "-q"},
		{diagnosticDocker, diagnosticExec, c.Node(), diagnosticCRICTL, "ps", "-a", "--name", "kube-apiserver", "-q"},
	}
	for _, command := range commands {
		out, err := c.diagnostic(ctx, command...)
		logf("%s:\n%s%v", strings.Join(command, " "), tail(out), err)
		if len(command) > 4 && command[3] == "crictl" && err == nil {
			for id := range strings.FieldsSeq(out) {
				logs, logErr := c.diagnostic(
					ctx,
					diagnosticDocker,
					diagnosticExec,
					c.Node(),
					diagnosticCRICTL,
					"logs",
					"--tail=200",
					id,
				)
				logf("%s container %s:\n%s%v", command[7], id, tail(logs), logErr)
			}
		}
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
	for _, args := range [][]string{
		{diagnosticGet, diagnosticPods, "-o=custom-columns=NAME:.metadata.name,PHASE:.status.phase," +
			"CONTAINERS:.status.containerStatuses[*].name,READY:.status.containerStatuses[*].ready," +
			"RESTARTS:.status.containerStatuses[*].restartCount,WAITING:.status.containerStatuses[*].state.waiting.reason," +
			"TERMINATED:.status.containerStatuses[*].state.terminated.reason," +
			"EXIT:.status.containerStatuses[*].state.terminated.exitCode"},
		{diagnosticGet, diagnosticEvents, diagnosticEventSort},
	} {
		command := append([]string{diagnosticKubectl, diagnosticContext, c.kubeContext, "-n", namespace}, args...)
		out, err := c.diagnostic(ctx, command...)
		tb.Logf("%s:\n%s%v", strings.Join(command, " "), tail(out), err)
	}
}
