// Package telemetry runs the suites of an install with the OpenTelemetry collectors and ingest
// tokens, whose registry trusts only the API's registry tokens.
package telemetry

import (
	"os"
	"testing"

	"github.com/team-loco/loco/tests/e2e/platform"
)

var p = platform.New(platform.Features{Name: "telemetry", Builds: true, Observability: true, RegistryToken: true})

func TestMain(m *testing.M) {
	os.Exit(p.Run(m))
}
