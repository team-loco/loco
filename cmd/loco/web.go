package loco

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/session"
)

var webCmd = &cobra.Command{
	Use:   "web [dashboard|resources|events|observability|usage|settings|profile|tokens|organizations|team]",
	Short: "Open loco pages in your browser",
	Long:  "Open loco pages in your browser. Defaults to dashboard if no argument provided.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return webCmdFunc(cmd, args)
	},
}

func webCmdFunc(cmd *cobra.Command, args []string) error {
	host, err := getHost(cmd)
	if err != nil {
		return err
	}

	page := ""
	if len(args) > 0 {
		page = args[0]
	}

	// Load config to get current org/workspace for workspace-scoped routes
	cfg, cfgErr := session.Load()
	var orgID, workspaceID string
	if cfgErr == nil {
		if scope, scopeErr := cfg.GetScope(); scopeErr == nil {
			orgID = scope.Organization.ID
			workspaceID = scope.Workspace.ID
		}
	}

	var path string
	switch page {
	case "dashboard", "":
		path = buildWorkspacePath(orgID, workspaceID, "")
	case "resources":
		path = buildWorkspacePath(orgID, workspaceID, "/resources")
	case "events":
		path = buildWorkspacePath(orgID, workspaceID, "/events")
	case "observability":
		path = buildWorkspacePath(orgID, workspaceID, "/observability")
	case "usage":
		path = buildWorkspacePath(orgID, workspaceID, "/usage")
	case "settings":
		path = buildWorkspacePath(orgID, workspaceID, "/settings")
	case "profile", "account":
		path = "/profile"
	case "tokens":
		path = "/tokens"
	case "organizations", "orgs":
		path = "/organizations"
	case "team":
		path = "/team"
	default:
		return fmt.Errorf("invalid page: %s. Valid options are: dashboard, resources, events, observability, usage, settings, profile, tokens, organizations, team", page)
	}

	url := host + path

	displayPage := page
	if displayPage == "" {
		displayPage = "dashboard"
	}

	slog.Debug("opening url in browser", "url", url, "page", displayPage)

	if err := openBrowser(url); err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}

	fmt.Printf("Opening %s in your browser...\n", displayPage)

	return nil
}

func openBrowser(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to execute browser command: %w", err)
	}

	return nil
}

func init() {
	webCmd.Flags().String("host", "", "Set the host URL")
}

func buildWorkspacePath(orgID, workspaceID string, subpath string) string {
	if orgID == "" || workspaceID == "" {
		return "/dashboard"
	}
	return fmt.Sprintf("/org/%s/wks/%s%s", orgID, workspaceID, subpath)
}
