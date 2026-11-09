package auth

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/host"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/rpc"
)

var (
	errTokenHostRequired = errors.New("set LOCO_HOST to the API URL when using LOCO_TOKEN")
	errTokenHostInvalid  = errors.New("LOCO_HOST must be an HTTP or HTTPS API URL when using LOCO_TOKEN")
	errTokenHostMismatch = errors.New("--host must match LOCO_HOST when using LOCO_TOKEN")
)

// ResolveLocoLogin prefers the environment credential for loco RPC commands.
// Session management commands and locoadm keep their stored login.
func ResolveLocoLogin(cmd *cobra.Command) (Login, error) {
	token := os.Getenv("LOCO_TOKEN")
	if token == "" {
		return ResolveLogin(cmd)
	}

	tokenHost := strings.TrimRight(os.Getenv(host.LocoHostEnv), "/")
	if tokenHost == "" {
		return Login{}, errTokenHostRequired
	}
	u, err := url.Parse(tokenHost)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return Login{}, errTokenHostInvalid
	}
	apiHost, err := host.GetHost(cmd)
	if err != nil {
		return Login{}, err
	}
	if strings.TrimRight(apiHost, "/") != tokenHost {
		return Login{}, errTokenHostMismatch
	}

	client := httputil.NewHTTPClient()
	// Redirects can forward the Authorization header to another origin.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return Login{Clients: rpc.NewHTTPClients(client, tokenHost, token), APIToken: true}, nil
}
