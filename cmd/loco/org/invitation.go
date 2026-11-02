package org

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/internal/session"
)

const (
	invitationTimeLayout = "2006-01-02 15:04"
	orgFlag              = "org"
)

type invitationDeps struct {
	NewOrgClient func(host, token string) orgv1connect.OrgServiceClient
	Output       io.Writer
}

func realOrgClient(host, token string) orgv1connect.OrgServiceClient {
	clients := rpc.ForHost(host, token)
	return clients.Org
}

func addOrgFlags(cmd *cobra.Command) {
	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().String(orgFlag, "", "Organization name (defaults to LOCO_ORG or the current scope)")
}

func invitationClient(cmd *cobra.Command, deps invitationDeps) (orgv1connect.OrgServiceClient, error) {
	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		return nil, err
	}
	locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
	if err != nil {
		return nil, err
	}
	return deps.NewOrgClient(host, locoToken.Token), nil
}

func resolveOrgID(cmd *cobra.Command, client orgv1connect.OrgServiceClient) (string, error) {
	name, err := cmdutil.ResolveOrg(cmd, session.Load)
	if err != nil {
		return "", err
	}
	resp, err := client.GetOrg(cmd.Context(), &orgv1.GetOrgRequest{
		Key: &orgv1.GetOrgRequest_OrgName{OrgName: name},
	})
	if err != nil {
		return "", fmt.Errorf("organization %q not found: %w", name, err)
	}
	return resp.GetOrganization().GetId(), nil
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
