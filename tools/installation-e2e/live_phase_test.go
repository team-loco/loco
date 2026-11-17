package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOperatorForwardsOnlyCompleteKnownPhasesBeforeExit(t *testing.T) {
	fakeOperatorCommand(t, `#!/bin/sh
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
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	previous := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = previous }()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	output := make(chan string, 1)
	dir := t.TempDir()
	go func() {
		buffer := bufio.NewReader(reader)
		line, readErr := buffer.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			t.Error(readErr)
		}
		if writeErr := os.WriteFile(ack, nil, 0600); writeErr != nil {
			t.Error(writeErr)
			cancel()
		}
		rest, readErr := io.ReadAll(buffer)
		if readErr != nil {
			t.Error(readErr)
		}
		output <- line + string(rest)
	}()
	_, runErr := operatorWorkflow(ctx, dir)
	if closeErr := writer.Close(); closeErr != nil {
		t.Error(closeErr)
	}
	forwarded := <-output
	phases, _, _ := strings.Cut(forwarded, dnsDiagnosticPrefix)
	if phases != want || strings.Contains(forwarded, "credential-canary") {
		t.Errorf("forwarded output = %q, want phases %q followed by safe DNS evidence", forwarded, want)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "phase network-policy, login failure api-unavailable") {
		t.Errorf("operator failure lost its safe phase/category: %v", runErr)
	}
}
