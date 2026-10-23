package main

import (
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/locoadm/cluster"
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "locoadm",
		Short: "The CLI for operating a Loco installation",
	}
	root.AddCommand(cluster.BuildClusterCmd())
	return root
}
