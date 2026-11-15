// Package builds runs the suites that need a worker that builds images, against one install with
// builds enabled and the static registry accounts.
package builds

import (
	"os"
	"testing"

	"github.com/team-loco/loco/tests/e2e/platform"
)

var p = platform.New(platform.Features{Name: "builds", Builds: true})

func TestMain(m *testing.M) {
	os.Exit(p.Run(m))
}
