package loco

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
)

var (
	errNoWorkspaces        = errors.New("no workspaces found")
	errUnexpectedSelection = errors.New("unexpected selection type")
	errInvalidScopeFormat  = errors.New("invalid format - expected <org-name>/<workspace-name>")
)

func newUseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use [org-name/workspace-name]",
		Short: "Switch to a different organization and workspace",
		Long: `Switch your current context to a different organization and workspace.

Run without arguments to interactively select from your available scopes.

Examples:
  loco use                        # interactive picker
  loco use my-org/my-workspace    # switch directly`,
		Args: cobra.MaximumNArgs(1),
		RunE: useCmdFunc,
	}
	cmd.Flags().String("host", "", "API host URL")
	return cmd
}

func useCmdFunc(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		return err
	}

	t, err := cmdutil.GetCurrentLocoToken(cmd)
	if err != nil {
		return err
	}

	apiClient := client.NewClient(host, t.Token)

	currentUser, err := apiClient.GetCurrentUser(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	orgs, err := apiClient.GetCurrentUserOrgs(ctx, currentUser.GetId())
	if err != nil {
		return fmt.Errorf("failed to get organizations: %w", err)
	}

	workspaces, err := apiClient.GetUserWorkspaces(ctx, currentUser.GetId())
	if err != nil {
		return fmt.Errorf("failed to get workspaces: %w", err)
	}

	cfg, err := session.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	var orgName, workspaceName string

	if len(args) != 1 {
		// build a flat list of org/workspace pairs for the picker
		type scopeOption struct {
			orgID         string
			orgName       string
			workspaceID   string
			workspaceName string
		}

		orgByID := make(map[string]string, len(orgs))
		for _, o := range orgs {
			orgByID[o.GetId()] = o.GetName()
		}

		var options []ui.SelectOption
		for _, ws := range workspaces {
			oName, ok := orgByID[ws.GetOrgId()]
			if !ok {
				continue
			}
			label := fmt.Sprintf("%s / %s", oName, ws.GetName())
			options = append(options, ui.SelectOption{
				Label:       label,
				Description: fmt.Sprintf("org: %s  workspace: %s", ws.GetOrgId(), ws.GetId()),
				Value: scopeOption{
					orgID:         ws.GetOrgId(),
					orgName:       oName,
					workspaceID:   ws.GetId(),
					workspaceName: ws.GetName(),
				},
			})
		}

		if len(options) == 0 {
			return errNoWorkspaces
		}

		selected, selErr := ui.SelectFromList("Select a scope", options)
		if selErr != nil {
			return fmt.Errorf("selection canceled: %w", selErr)
		}

		scope, ok := selected.(scopeOption)
		if !ok {
			return errUnexpectedSelection
		}

		if err := cfg.SetDefaultScope(
			session.SimpleOrg{ID: scope.orgID, Name: scope.orgName},
			session.SimpleWorkspace{ID: scope.workspaceID, Name: scope.workspaceName},
		); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		printSwitched(scope.orgName, scope.workspaceName)
		return nil
	}

	parts := strings.Split(args[0], "/")
	if len(parts) != 2 {
		return errInvalidScopeFormat
	}
	orgName = parts[0]
	workspaceName = parts[1]

	// direct switch via argument — look up IDs
	var orgID string
	for _, o := range orgs {
		if o.GetName() == orgName {
			orgID = o.GetId()
			break
		}
	}
	if orgID == "" {
		return fmt.Errorf("organization %q not found", orgName)
	}

	var workspaceID string
	for _, ws := range workspaces {
		if ws.GetName() == workspaceName && ws.GetOrgId() == orgID {
			workspaceID = ws.GetId()
			break
		}
	}
	if workspaceID == "" {
		return fmt.Errorf("workspace %q not found in organization %q", workspaceName, orgName)
	}

	if err := cfg.SetDefaultScope(
		session.SimpleOrg{ID: orgID, Name: orgName},
		session.SimpleWorkspace{ID: workspaceID, Name: workspaceName},
	); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	printSwitched(orgName, workspaceName)
	return nil
}

func printSwitched(orgName, workspaceName string) {
	msg := lipgloss.NewStyle().
		Foreground(ui.Ok).
		Render(fmt.Sprintf("✓ Switched to %s/%s", orgName, workspaceName))
	fmt.Println(msg)
}
