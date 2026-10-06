package loco

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/ui"
)

const (
	versionCheckTimeout = time.Second
	installCommand      = "curl -fsSL https://loco.build/install.sh | sh"
	releasesURL         = "https://github.com/team-loco/loco/releases"
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
		client := configv1connect.NewConfigServiceClient(httputil.NewHTTPClient(), host)
		req := connect.NewRequest(&configv1.GetConfigRequest{})
		resp, err := client.GetConfig(ctx, req)
		if err != nil {
			slog.Debug("could not fetch server config", "error", err)
			return
		}
		c.minimum = resp.Msg.GetMinCliVersion()
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
	style := lipgloss.NewStyle().Foreground(ui.Warn)
	styled := style.Render(hint)
	message := fmt.Sprintf("\n%s\n  Update:    loco update\n  Changelog: %s\n", styled, releasesURL)
	if _, err := lipgloss.Fprint(w, message); err != nil {
		slog.Debug("could not print upgrade hint", "error", err)
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
