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

// ParseURL parses an absolute HTTP or HTTPS address, with an error naming source and the URL to type.
func ParseURL(source, value string) (*url.URL, error) {
	if value == "" {
		return nil, fmt.Errorf("set %s to the server's URL, such as https://api.loco.build", source)
	}
	if u, err := url.Parse(value); err == nil && isEndpoint(u) {
		return u, nil
	}
	if isBareHost(value) {
		return nil, fmt.Errorf("set %s to a URL such as https://%s", source, value)
	}
	return nil, fmt.Errorf(
		"set %s to an http:// or https:// URL without credentials, query or fragment, not %q", source, value,
	)
}

func checked(source, value string) (string, error) {
	if _, err := ParseURL(source, value); err != nil {
		return "", err
	}
	return value, nil
}

func isEndpoint(u *url.URL) bool {
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Hostname() == "" || u.User != nil {
		return false
	}
	if u.RawQuery != "" || u.ForceQuery {
		return false
	}
	return u.Fragment == ""
}

// isBareHost reports whether value is a host or host:port with nothing else, so https:// makes it a URL.
func isBareHost(value string) bool {
	u, err := url.Parse("https://" + value)
	if err != nil || u.Host != value {
		return false
	}
	return isEndpoint(u) && !strings.HasSuffix(value, ":")
}
