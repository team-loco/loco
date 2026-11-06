package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCancelledNetworkProbeCleansBothOwnedPodsAndReportsCleanupFailure(t *testing.T) {
	for _, exit := range []string{"0", "1"} {
		t.Run(exit, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "cleanup")
			fakeOperatorCommand(t, `#!/bin/sh
case "$*" in
 *"delete pod installation-network-probe"*)
  printf '%s\n' "$*" >> "$PROBE_CLEANUP_MARKER"
  exit "$PROBE_CLEANUP_EXIT"
  ;;
esac
exit 1
`)
			t.Setenv("E2E_PROBE_IMAGE", "fixture-image")
			t.Setenv("E2E_BUILD_NAMESPACE", "fixture-builds")
			t.Setenv("PROBE_CLEANUP_MARKER", marker)
			t.Setenv("PROBE_CLEANUP_EXIT", exit)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			err := probeNetworkPolicy(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("lost probe cancellation: %v", err)
			}
			cleanup, readErr := os.ReadFile(marker)
			if readErr != nil {
				t.Fatal("failed probe left its pods behind")
			}
			for _, ns := range []string{fixtureNamespace, "fixture-builds"} {
				if !strings.Contains(string(cleanup), "-n "+ns+" delete pod installation-network-probe") {
					t.Fatal("failed probe did not clean both owned namespaces")
				}
			}
			if got := strings.Contains(err.Error(), "cleanup network-policy probe"); got != (exit == "1") {
				t.Fatalf("cleanup failure visibility: %v", err)
			}
		})
	}
}
