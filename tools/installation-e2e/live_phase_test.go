package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOperatorForwardsOnlyCompleteKnownPhasesBeforeExit(t *testing.T) {
	fakeCommand(t, kubectlExecutable, `#!/bin/sh
printf '%s' "$OPERATOR_TEST_OUTPUT"
while [ ! -s "$OPERATOR_TEST_CAPTURE" ]; do /bin/sleep 0.01; done
printf 'installation-e2e login failure: api-unavailable\n'
printf 'installation-e2e phase: workers'
exit 1
`)
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
		t.Setenv("OPERATOR_TEST_CAPTURE", os.Stderr.Name())
		_, runErr = operatorWorkflow(ctx, t.TempDir())
	})
	phases, _, _ := strings.Cut(forwarded, dnsDiagnosticPrefix)
	if phases != want || strings.Contains(forwarded, "credential-canary") {
		t.Errorf("forwarded output = %q, want phases %q followed by safe DNS evidence", forwarded, want)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "phase network-policy, login failure api-unavailable") {
		t.Errorf("operator failure lost its safe phase/category: %v", runErr)
	}
}
