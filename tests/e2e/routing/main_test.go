package routing

import (
	"os"
	"testing"

	"github.com/team-loco/loco/tests/e2e/platform"
)

var p = platform.New(platform.Features{Name: "routing"})

func TestMain(m *testing.M) { os.Exit(p.Run(m)) }
