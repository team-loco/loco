package controlplane

import (
	"net/http"
	"strings"
	"testing"
)

// checkRegistry asks the bundled registry for its API root without a token. A registry that loaded
// the API's signing key answers with a Bearer challenge naming the API's token endpoint and the
// registry's service.
//
//nolint:thelper // a step of TestControlPlane
func (cp *controlPlane) checkRegistry(t *testing.T) {
	root := cp.request(t, http.MethodGet, cp.values.Registry.Hostname, "/v2/", "", nil)
	root.expect(t, http.StatusUnauthorized)
	challenge := root.header.Get("WWW-Authenticate")
	for _, want := range []string{
		"Bearer ",
		`realm="http://` + cp.values.ControlPlaneHostname + `/registry/token"`,
		`service="` + cp.values.Registry.Hostname + `"`,
	} {
		if !strings.Contains(challenge, want) {
			t.Errorf("the registry challenges with %q, want it to contain %q", challenge, want)
		}
	}
}
