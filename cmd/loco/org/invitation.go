package org

import (
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/session"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	invitationTimeLayout = "2006-01-02 15:04"
	orgFlag              = "org"
)

func addOrgFlags(cmd *cobra.Command) {
	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().String(orgFlag, "", "Organization name or ID (defaults to LOCO_ORG or the current scope)")
}

func resolveOrgID(cmd *cobra.Command, clients *rpc.Clients) (string, error) {
	ref, err := cmdutil.ResolveOrg(cmd, session.Load)
	if err != nil {
		return "", err
	}
	org, err := findOrg(cmd.Context(), clients, ref)
	if err != nil {
		return "", err
	}
	return org.GetId(), nil
}
