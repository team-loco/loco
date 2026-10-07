package resource

import (
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/infra"
)

func BuildDeployCmd() *cobra.Command { return infra.BuildDeployCmd() }
