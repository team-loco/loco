package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

const (
	fixtureDiagnosticTimeout = 30 * time.Second
	kubectlGet               = "get"
)

func (f *fixture) waitFixture(ctx context.Context, name string) error {
	if _, err := command(ctx, f.root, nil, "kubectl", "-n", fixtureNamespace,
		"rollout", "status", "deployment/"+name, "--timeout="+fixtureReadyTimeout); err != nil {
		f.fixtureDiagnostics(ctx, name)
		return fmt.Errorf("fixture deployment %s/%s readiness: %w", fixtureNamespace, name, err)
	}
	return nil
}

func (f *fixture) fixtureDiagnostics(ctx context.Context, name string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fixtureDiagnosticTimeout)
	defer cancel()
	// Project status fields; messages, pod specs and fixture configuration contain credentials.
	for _, args := range [][]string{
		{"pods", "-l", "fixture=" + name, "-o=custom-columns=NAME:.metadata.name,PHASE:.status.phase," +
			"CONDITIONS:.status.conditions[*].type,STATUS:.status.conditions[*].status," +
			"REASON:.status.conditions[*].reason,WAITING:.status.containerStatuses[*].state.waiting.reason," +
			"RESTARTS:.status.containerStatuses[*].restartCount"},
		{"events", "--sort-by=.metadata.creationTimestamp",
			"-o=custom-columns=TIME:.metadata.creationTimestamp,OBJECT:.involvedObject.name," +
				"TYPE:.type,REASON:.reason,COUNT:.count"},
	} {
		out, err := command(
			ctx,
			f.root,
			nil,
			"kubectl",
			append([]string{"-n", fixtureNamespace, kubectlGet}, args...)...)
		fmt.Fprintf(os.Stderr, "Fixture %s %s:\n%s", name, args[0], out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "diagnostic query failed: %v\n", err)
		}
	}
}
