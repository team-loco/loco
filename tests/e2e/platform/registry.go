package platform

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Account is a registry account of dev/registry/htpasswd.
type Account struct {
	Name     string
	Password string
}

// The static registry accounts the API, the kind nodes and builds use.
var (
	RegistryAPIUser     = Account{"api", "loco-dev-api"}
	RegistryNodesUser   = Account{"nodes", "loco-dev-nodes"}
	RegistryBuilderUser = Account{"builder", "loco-dev-builder"}
)

var manifestTypes = []string{
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}

// RegistryResponse is the registry's answer to one request.
type RegistryResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// Challenge is the response's WWW-Authenticate header.
func (r RegistryResponse) Challenge() string {
	return r.Header.Get("WWW-Authenticate")
}

// RegistryRequest sends method path to the registry from the host, with a bearer token when token
// is set or basic auth when account is set.
func (p *Platform) RegistryRequest(
	ctx context.Context,
	method, path string,
	account *Account,
	token string,
) (RegistryResponse, error) {
	request, err := http.NewRequestWithContext(ctx, method, p.RegistryURL+path, nil)
	if err != nil {
		return RegistryResponse{}, err
	}
	for _, accept := range manifestTypes {
		request.Header.Add("Accept", accept)
	}
	if account != nil {
		request.SetBasicAuth(account.Name, account.Password)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return RegistryResponse{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return RegistryResponse{}, err
	}
	return RegistryResponse{Status: response.StatusCode, Header: response.Header, Body: body}, nil
}

// ManifestStatus is the HTTP status of a manifest GET as account, or anonymous without one.
func (p *Platform) ManifestStatus(ctx context.Context, account *Account, repository, digest string) (int, error) {
	response, err := p.RegistryRequest(ctx, http.MethodGet, "/v2/"+repository+"/manifests/"+digest, account, "")
	return response.Status, err
}

// RealmResponse is the answer of the API's registry token endpoint.
type RealmResponse struct {
	Status int
	Token  string
}

// RealmToken asks the API's token endpoint for scopes as user with password.
func (p *Platform) RealmToken(ctx context.Context, user, password string, scopes ...string) (RealmResponse, error) {
	query := url.Values{"service": {p.RegistryService}, "scope": scopes}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.APIURL+"/registry/token?"+query.Encode(), nil)
	if err != nil {
		return RealmResponse{}, err
	}
	request.SetBasicAuth(user, password)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return RealmResponse{}, err
	}
	defer response.Body.Close()
	var body struct {
		Token string `json:"token"`
	}
	if response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			return RealmResponse{}, fmt.Errorf("decode the registry token: %w", err)
		}
	}
	return RealmResponse{Status: response.StatusCode, Token: body.Token}, nil
}

// Access is one entry of a registry token's access claim.
type Access struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

// TokenAccess decodes the access claim of a registry token.
func TokenAccess(token string) ([]Access, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("a registry token has three parts, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims struct {
		Access []Access `json:"access"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims.Access, nil
}

// DockerConfigAuth is one registry's entry of a dockerconfigjson Secret.
type DockerConfigAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
}

// RegistrySecret reads the registry credential a dockerconfigjson Secret holds.
func (p *Platform) RegistrySecret(ctx context.Context, namespace, name string) (DockerConfigAuth, error) {
	out, err := p.Kubectl(ctx, "-n", namespace, "get", "secret", name, "-o", `jsonpath={.data.\.dockerconfigjson}`)
	if err != nil {
		return DockerConfigAuth{}, err
	}
	data, err := base64.StdEncoding.DecodeString(out)
	if err != nil {
		return DockerConfigAuth{}, err
	}
	var config struct {
		Auths map[string]DockerConfigAuth `json:"auths"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return DockerConfigAuth{}, err
	}
	for _, auth := range config.Auths {
		if auth.Password != "" {
			return auth, nil
		}
	}
	return DockerConfigAuth{}, fmt.Errorf("secret %s/%s holds no registry password", namespace, name)
}
