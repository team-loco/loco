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
printf 'installation-e2e bootstrap failure: api-unavailable\n'
printf 'installation-e2e phase: workers'
exit 1
`)
	ack := filepath.Join(t.TempDir(), "ack")
	t.Setenv("OPERATOR_TEST_ACK", ack)
	t.Setenv("OPERATOR_TEST_OUTPUT", "credential-canary\n"+
		phasePrefix+phaseWorkers+" credential-canary\n"+
		phasePrefix+strings.Repeat("x", 8192)+"\n"+
		phasePrefix+phasePreflight+"\n")
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
	first := make(chan string, 1)
	all := make(chan string, 1)
	go func() {
		buffer := bufio.NewReader(reader)
		line, readErr := buffer.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			t.Error(readErr)
		}
		first <- line
		rest, readErr := io.ReadAll(buffer)
		if readErr != nil {
			t.Error(readErr)
		}
		all <- line + string(rest)
	}()
	done := make(chan error, 1)
	dir := t.TempDir()
	go func() {
		_, runErr := operatorWorkflow(ctx, dir)
		done <- runErr
	}()
	var live string
	select {
	case live = <-first:
		if writeErr := os.WriteFile(ack, nil, 0600); writeErr != nil {
			t.Error(writeErr)
			cancel()
		}
	case <-ctx.Done():
	}
	runErr := <-done
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	want := phasePrefix + phasePreflight + "\n"
	if live != want {
		t.Errorf("live phase = %q, want %q before operator exit", live, want)
	}
	if output := <-all; output != want {
		t.Errorf("forwarded output = %q, want only %q", output, want)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "phase preflight, bootstrap failure api-unavailable") {
		t.Errorf("operator failure lost its safe phase/category: %v", runErr)
	}
}
