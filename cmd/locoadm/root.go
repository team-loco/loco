package main

import (
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/locoadm/bootstrap"
	"github.com/team-loco/loco/cmd/locoadm/cluster"
	"github.com/team-loco/loco/cmd/locoadm/user"
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "locoadm",
		Short: "The CLI for operating a Loco installation",
	}
	root.AddCommand(cluster.BuildClusterCmd())
	root.AddCommand(bootstrap.BuildBootstrapCmd())
	root.AddCommand(user.BuildUserCmd())
	return root
}
