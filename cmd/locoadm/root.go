package main

import (
	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/locoadm/bootstrap"
	"github.com/team-loco/loco/cmd/locoadm/cluster"
	"github.com/team-loco/loco/cmd/locoadm/install"
	"github.com/team-loco/loco/cmd/locoadm/org"
	"github.com/team-loco/loco/cmd/locoadm/recovery"
	"github.com/team-loco/loco/cmd/locoadm/user"
	"github.com/team-loco/loco/cmd/locoadm/validate"
	"github.com/team-loco/loco/cmd/locoadm/verify"
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "locoadm",
		Short: "The CLI for operating a Loco installation",
	}
	root.AddCommand(cluster.BuildClusterCmd())
	root.AddCommand(bootstrap.BuildBootstrapCmd())
	root.AddCommand(user.BuildUserCmd())
	root.AddCommand(org.BuildOrgCmd())
	root.AddCommand(install.Commands()...)
	root.AddCommand(recovery.Command(), verify.Command())
	root.AddCommand(validate.BuildValidateCmd())
	return root
}
