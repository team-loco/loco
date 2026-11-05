package org

import (
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/internal/session"
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

func statusText(invitation *orgv1.Invitation) string {
	switch invitation.GetStatus() {
	case orgv1.InvitationStatus_INVITATION_STATUS_EMAILED:
		return "emailed"
	case orgv1.InvitationStatus_INVITATION_STATUS_FAILED:
		return "email failed: " + invitation.GetDeliveryError()
	case orgv1.InvitationStatus_INVITATION_STATUS_NO_TRANSPORT:
		return "no email transport configured"
	case orgv1.InvitationStatus_INVITATION_STATUS_EXPIRED:
		return "expired"
	case orgv1.InvitationStatus_INVITATION_STATUS_UNSPECIFIED:
		return "unknown"
	default:
		return "unknown"
	}
}
