package main

import (
	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/groupcmd"
	"github.com/team-loco/loco/cmd/internal/update"
	"github.com/team-loco/loco/cmd/locoadm/admin"
	"github.com/team-loco/loco/cmd/locoadm/audit"
	"github.com/team-loco/loco/cmd/locoadm/cluster"
	"github.com/team-loco/loco/cmd/locoadm/initialize"
	"github.com/team-loco/loco/cmd/locoadm/login"
	"github.com/team-loco/loco/cmd/locoadm/org"
	"github.com/team-loco/loco/cmd/locoadm/recovery"
	"github.com/team-loco/loco/cmd/locoadm/region"
	"github.com/team-loco/loco/cmd/locoadm/status"
	"github.com/team-loco/loco/cmd/locoadm/user"
	"github.com/team-loco/loco/cmd/locoadm/verify"
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "locoadm",
		Short: "The CLI for operating a Loco installation",
	}
	root.AddCommand(initialize.Command(), status.Command())
	root.AddCommand(admin.BuildAdminCmd())
	root.AddCommand(cluster.BuildClusterCmd(), region.BuildRegionCmd())
	root.AddCommand(login.Command(), update.Command("locoadm"))
	root.AddCommand(user.BuildUserCmd())
	root.AddCommand(org.BuildOrgCmd())
	root.AddCommand(audit.BuildAuditCmd())
	root.AddCommand(recovery.Command(), verify.Command())
	groupcmd.RejectUnknown(root)
	return root
}
