package host

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/session"
)

const (
	LocoHostEnv     = "LOCO_HOST"
	DefaultLocoHost = "https://api.loco.build"
	DefaultWebHost  = "https://loco.build"
)

// GetHost resolves the API host from flag > env > config file > default.
func GetHost(cmd *cobra.Command) (string, error) {
	if cmd.Flags().Lookup("host") != nil {
		host, err := cmd.Flags().GetString("host")
		if err != nil {
			return "", fmt.Errorf("error reading host flag: %w", err)
		}
		if host != "" {
			slog.Debug("using host from flag")
			return checked("--host", host)
		}
	}

	host := os.Getenv(LocoHostEnv)
	if host != "" {
		slog.Debug("using host from environment variable")
		return checked(LocoHostEnv, host)
	}

	cfg, err := session.Load()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	if cfg.LocoHost != "" {
		slog.Debug("using host from config file")
		return checked("locoHost in ~/.loco/"+session.ConfigFileName, cfg.LocoHost)
	}

	slog.Debug("defaulting to prod url")
	return DefaultLocoHost, nil
}

// GetWebHost resolves the web UI host from flag > env > config file > default.
func GetWebHost(cmd *cobra.Command) (string, error) {
	host, err := cmd.Flags().GetString("web-host")
	if err != nil {
		return "", fmt.Errorf("error reading web-host flag: %w", err)
	}
	if host != "" {
		return checked("--web-host", host)
	}

	host = os.Getenv("LOCO_WEB_HOST")
	if host != "" {
		return checked("LOCO_WEB_HOST", host)
	}

	cfg, err := session.Load()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	if cfg.WebHost != "" {
		return checked("webHost in ~/.loco/"+session.ConfigFileName, cfg.WebHost)
	}

	return DefaultWebHost, nil
}

// ParseURL parses an API or dashboard address, rejecting one that is not an absolute HTTP or
// HTTPS URL; the error names source (a flag, variable or config key) and, for a bare host, the URL to type.
func ParseURL(source, value string) (*url.URL, error) {
	if !strings.Contains(value, "://") {
		return nil, fmt.Errorf("set %s to a URL such as https://%s", source, value)
	}
	u, err := url.Parse(value)
	if err != nil || !isEndpoint(u) {
		return nil, fmt.Errorf(
			"set %s to an http:// or https:// URL without credentials, query or fragment, not %q", source, value,
		)
	}
	return u, nil
}

func checked(source, value string) (string, error) {
	if _, err := ParseURL(source, value); err != nil {
		return "", err
	}
	return value, nil
}

func isEndpoint(u *url.URL) bool {
	if u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}
