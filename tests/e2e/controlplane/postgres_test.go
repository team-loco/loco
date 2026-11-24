package controlplane

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/team-loco/loco/tests/e2e/platform"
)

// checkBackup waits for the ScheduledBackup's immediate base backup of the chart's CloudNativePG
// cluster to reach the fixture bucket.
//
//nolint:thelper // a step of TestControlPlane
func (cp *controlPlane) checkBackup(t *testing.T) {
	platform.Eventually(t, installTimeout, "a completed Postgres base backup", func(ctx context.Context) error {
		phases, err := c.Kubectl(ctx, "-n", cp.namespace, "get", "backups.postgresql.cnpg.io",
			"-l", "cnpg.io/cluster="+postgresCluster, "-o", "jsonpath={.items[*].status.phase}")
		if err != nil {
			return err
		}
		if !strings.Contains(" "+phases+" ", " completed ") {
			return fmt.Errorf("backup phases %q", phases)
		}
		return nil
	})
}
