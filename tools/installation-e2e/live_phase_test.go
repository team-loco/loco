package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOperatorForwardsOnlyCompleteKnownPhasesBeforeExit(t *testing.T) {
	fakeCommand(t, kubectlExecutable, `#!/bin/sh
printf '%s' "$OPERATOR_TEST_OUTPUT"
while [ ! -f "$OPERATOR_TEST_ACK" ]; do /bin/sleep 0.01; done
printf 'installation-e2e login failure: api-unavailable\n'
printf 'installation-e2e phase: workers'
exit 1
`)
	ack := filepath.Join(t.TempDir(), "ack")
	t.Setenv("OPERATOR_TEST_ACK", ack)
	want := phasePrefix + "source-deploy\n" + phasePrefix + "source-https\n" +
		phasePrefix + "source-verify\n" + phasePrefix + "network-policy\n"
	t.Setenv("OPERATOR_TEST_OUTPUT", "credential-canary\n"+
		phasePrefix+phaseWorkers+" credential-canary\n"+
		phasePrefix+"unknown-stage\n"+
		phasePrefix+strings.Repeat("x", 8192)+"\n"+
		want)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var runErr error
	forwarded := captureOutput(t, func() {
		acked := make(chan struct{})
		go func(capture string) {
			defer close(acked)
			// Acknowledges the first forwarded line, which the fake waits for before it exits.
			for ctx.Err() == nil {
				data, readErr := os.ReadFile(capture)
				if readErr != nil {
					t.Error(readErr)
					return
				}
				if bytes.ContainsRune(data, '\n') {
					if writeErr := os.WriteFile(ack, nil, 0600); writeErr != nil {
						t.Error(writeErr)
						cancel()
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}(os.Stderr.Name())
		_, runErr = operatorWorkflow(ctx, t.TempDir())
		<-acked
	})
	phases, _, _ := strings.Cut(forwarded, dnsDiagnosticPrefix)
	if phases != want || strings.Contains(forwarded, "credential-canary") {
		t.Errorf("forwarded output = %q, want phases %q followed by safe DNS evidence", forwarded, want)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "phase network-policy, login failure api-unavailable") {
		t.Errorf("operator failure lost its safe phase/category: %v", runErr)
	}
}
