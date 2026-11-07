package main

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/team-loco/loco/cmd/internal/host"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/session"
)

const (
	pageDashboard     = "dashboard"
	organizationsPath = "/organizations"
)

// webPage is a page `loco web` can open. path builds the URL path from the
// current scope; aliases are extra names that open the same page.
type webPage struct {
	name    string
	aliases []string
	path    func(orgID, workspaceID string) string
}

func workspacePage(subpath string) func(orgID, workspaceID string) string {
	return func(orgID, workspaceID string) string { return buildWorkspacePath(orgID, workspaceID, subpath) }
}

func fixedPage(path string) func(orgID, workspaceID string) string {
	return func(string, string) string { return path }
}

var webPages = []webPage{
	{name: pageDashboard, path: workspacePage("/dashboard")},
	{name: "resources", path: workspacePage("/resources")},
	{name: "create-resource", path: workspacePage("/create-resource")},
	{name: "events", path: workspacePage("/events")},
	{name: "observability", path: workspacePage("/observability")},
	{name: "usage", path: workspacePage("/usage")},
	{name: "settings", path: workspacePage("/settings")},
	{name: "org-settings", path: func(orgID, _ string) string { return buildOrgPath(orgID, "/settings") }},
	{name: "profile", aliases: []string{"account"}, path: fixedPage("/profile")},
	{name: "tokens", path: fixedPage("/tokens")},
	{name: "organizations", aliases: []string{"orgs"}, path: fixedPage(organizationsPath)},
	{name: "team", path: fixedPage("/team")},
}

// webPageNames lists every name `loco web` accepts, aliases included.
func webPageNames() []string {
	var names []string
	for _, p := range webPages {
		names = append(names, p.name)
		names = append(names, p.aliases...)
	}
	return names
}

func findWebPage(name string) (webPage, bool) {
	for _, p := range webPages {
		if p.name == name || slices.Contains(p.aliases, name) {
			return p, true
		}
	}
	return webPage{}, false
}

func newWebCmd() *cobra.Command {
	names := webPageNames()
	cmd := &cobra.Command{
		Use:   "web [page]",
		Short: "Open loco pages in your browser",
		Long: "Open loco pages in your browser. Defaults to dashboard if no page is given.\n\nPages: " +
			strings.Join(names, ", "),
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: names,
		RunE:      webCmdFunc,
	}
	cmd.Flags().String("web-host", "", "Web UI URL")
	return cmd
}

func webCmdFunc(cmd *cobra.Command, args []string) error {
	webHost, err := host.GetWebHost(cmd)
	if err != nil {
		return err
	}

	page := ""
	if len(args) > 0 {
		page = args[0]
	}

	cfg, err := session.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	var orgID, workspaceID string
	if scope, scopeErr := cfg.GetScope(); scopeErr == nil {
		orgID = scope.Organization.ID
		workspaceID = scope.Workspace.ID
	}

	if page == "" {
		page = pageDashboard
	}
	webPage, ok := findWebPage(page)
	if !ok {
		return fmt.Errorf("invalid page: %s. Valid options are: %s", page, strings.Join(webPageNames(), ", "))
	}

	url := webHost + webPage.path(orgID, workspaceID)

	slog.Debug("opening url in browser", "url", url, "page", page)

	ctx := cmd.Context()
	if err = openBrowser(ctx, url); err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}

	fmt.Printf("Opening %s in your browser...\n", page)

	return nil
}

func openBrowser(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
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

func buildWorkspacePath(orgID, workspaceID string, subpath string) string {
	if orgID == "" || workspaceID == "" {
		return "/dashboard"
	}
	return fmt.Sprintf("/org/%s/wks/%s%s", orgID, workspaceID, subpath)
}

func buildOrgPath(orgID string, subpath string) string {
	if orgID == "" {
		return organizationsPath
	}
	return fmt.Sprintf("/org/%s%s", orgID, subpath)
}
