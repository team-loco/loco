package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"time"

	"github.com/team-loco/loco/internal/rpc"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/internal/ui"
)

const (
	versionCheckTimeout  = time.Second
	latestReleaseTimeout = 3 * time.Second
	installCommand       = "curl -fsSL https://loco.build/install.sh | sh"
	releasesURL          = "https://github.com/team-loco/loco/releases"
	versionFlagName      = "version"
)

type versionCheck struct {
	done    chan struct{}
	current string
	minimum string
}

func (c *versionCheck) start(cmd *cobra.Command) {
	if c == nil || c.done != nil || cmd.Annotations[skipVersionCheckKey] != "" {
		return
	}
	current := cmd.Root().Version
	if !semver.IsValid(current) {
		return
	}
	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		slog.Debug("skipping cli version check", "error", err)
		return
	}
	c.current = current
	c.done = make(chan struct{})
	go func() {
		defer close(c.done)
		ctx, cancel := context.WithTimeout(context.Background(), versionCheckTimeout)
		defer cancel()
		configClients := rpc.ForHost(host, "")
		configClient := configClients.Config
		req := &configv1.GetConfigRequest{}
		resp, err := configClient.GetConfig(ctx, req)
		if err != nil {
			slog.Debug("could not fetch server config", "error", err)
			return
		}
		c.minimum = resp.GetMinCliVersion()
	}()
}

func (c *versionCheck) report(w io.Writer) {
	if c == nil || c.done == nil {
		return
	}
	<-c.done
	hint := upgradeHint(c.current, c.minimum)
	if hint == "" {
		return
	}
	printUpdateHint(w, hint, releasesURL)
}

func versionFlagSet(root *cobra.Command) bool {
	flag := root.Flags().Lookup(versionFlagName)
	return flag != nil && flag.Changed
}

func reportNewerRelease(ctx context.Context, w io.Writer, u updater, current string) {
	if !semver.IsValid(current) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, latestReleaseTimeout)
	defer cancel()
	latest, err := u.latestTag(ctx)
	if err != nil {
		slog.Debug("could not check for a newer cli release", "error", err)
		return
	}
	hint := newerReleaseHint(current, latest)
	if hint == "" {
		return
	}
	changelog, err := url.JoinPath(u.releasesURL, "tag", latest)
	if err != nil {
		slog.Debug("could not build changelog url", "error", err)
		return
	}
	printUpdateHint(w, hint, changelog)
}

func newerReleaseHint(current, latest string) string {
	if !semver.IsValid(current) || !semver.IsValid(latest) {
		return ""
	}
	if semver.Compare(current, latest) >= 0 {
		return ""
	}
	return fmt.Sprintf("A new version of loco is available: %s -> %s", current, latest)
}

func printUpdateHint(w io.Writer, hint, changelog string) {
	style := lipgloss.NewStyle().Foreground(ui.Warn)
	styled := style.Render(hint)
	message := fmt.Sprintf("\n%s\n  Run loco update\n  Changelog: %s\n", styled, changelog)
	if _, err := lipgloss.Fprint(w, message); err != nil {
		slog.Debug("could not print update hint", "error", err)
	}
}

func upgradeHint(current, minimum string) string {
	if !semver.IsValid(current) || !semver.IsValid(minimum) {
		return ""
	}
	if semver.Compare(current, minimum) >= 0 {
		return ""
	}
	return fmt.Sprintf("loco %s is no longer supported by this server; upgrade to %s or newer.", current, minimum)
}
