// Package apps runs the suites of an apps-only install (cluster.role=apps), which builds nothing.
package apps

import (
	"os"
	"testing"

	"github.com/team-loco/loco/tests/e2e/platform"
)

var p = platform.New(platform.Features{Name: "apps"})

func TestMain(m *testing.M) {
	os.Exit(p.Run(m))
}
