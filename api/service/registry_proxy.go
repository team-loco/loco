package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
)

type registryToken struct {
	Value   string
	Expires time.Time
}

func registryRoute(path string) (uuid.UUID, string, string, string, error) {
	parts := strings.Split(strings.TrimPrefix(path, "/v2/"), "/")
	if len(parts) < 5 || parts[0] != "loco" {
		return uuid.Nil, "", "", "", errors.New("unsupported registry repository")
	}
	environmentID, err := uuid.Parse(parts[1])
	if err != nil || !infraSecretName.MatchString(parts[2]) || !infraSecretName.MatchString(parts[3]) {
		return uuid.Nil, "", "", "", errors.New("invalid registry target")
	}
	operation := strings.Join(parts[4:], "/")
	if strings.Contains(path, "..") || (!strings.HasPrefix(operation, "blobs/") &&
		!strings.HasPrefix(operation, "manifests/") && operation != "tags/list") {
		return uuid.Nil, "", "", "", errors.New("unsupported registry operation")
	}
	return environmentID, parts[2], parts[3], operation, nil
}

func (s *RegistryServer) ProxyHandler() http.Handler {
	return http.HandlerFunc(s.serveRegistry)
}

func (s *RegistryServer) serveRegistry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
	_, token, ok := r.BasicAuth()
	if !ok {
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		ok = token != "" && token != r.Header.Get("Authorization")
	}
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="loco-registry"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	_, scopes, err := s.machine.GetToken(r.Context(), token)
	if err != nil {
		http.Error(w, "invalid registry credentials", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/v2/" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.URL.RawPath != "" {
		http.Error(w, "encoded repository paths are unsupported", http.StatusBadRequest)
		return
	}
	environmentID, stack, key, operation, err := registryRoute(r.URL.Path)
	if err != nil {
		http.Error(w, "invalid registry path", http.StatusBadRequest)
		return
	}
	restriction, restrictionErr := s.machine.StackRestriction(r.Context(), token)
	if restrictionErr != nil {
		http.Error(w, "invalid registry credentials", http.StatusUnauthorized)
		return
	}
	if restriction != nil {
		r = r.WithContext(context.WithValue(r.Context(), contextkeys.StackRestrictionKey, restriction))
	}
	if targetErr := tvm.VerifyStackTarget(r.Context(), environmentID, stack); targetErr != nil {
		http.Error(w, "registry stack is not authorized", http.StatusForbidden)
		return
	}
	scope, supported := registryOperationScope(r.Method, operation)
	if !supported {
		http.Error(w, "unsupported registry method", http.StatusMethodNotAllowed)
		return
	}
	if verifyWithGivenEntityScopesErr := s.machine.VerifyWithGivenEntityScopes(r.Context(), scopes, db.EntityScope{
		EntityType: db.EntityTypeEnvironment, EntityID: environmentID, Scope: scope,
	}); verifyWithGivenEntityScopesErr != nil {
		http.Error(w, "registry target is not authorized", http.StatusForbidden)
		return
	}
	if _, getEnvironmentByIDErr := s.queries.GetEnvironmentByID(
		r.Context(),
		environmentID,
	); getEnvironmentByIDErr != nil {
		http.Error(w, "registry environment is unavailable", http.StatusNotFound)
		return
	}
	base, err := url.Parse("https://" + s.registryBaseImage)
	if err != nil || base.Host == "" || strings.Contains(base.Path, ":") {
		http.Error(w, "registry is not configured", http.StatusServiceUnavailable)
		return
	}
	physical := strings.Trim(base.Path, "/") + "/" + environmentID.String() + "/" + stack + "." + key
	virtual := "loco/" + environmentID.String() + "/" + stack + "/" + key
	auth, err := s.registryBearer(r.Context(), base, physical)
	if err != nil {
		http.Error(w, "upstream registry authentication failed", http.StatusBadGateway)
		return
	}
	upstream := &url.URL{Scheme: base.Scheme, Host: base.Host}
	proxy := &httputil.ReverseProxy{
		Transport: s.httpClient.Transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(upstream)
			request.Out.URL.Path = "/v2/" + physical + "/" + operation
			request.Out.URL.RawPath = ""
			query := request.Out.URL.Query()
			query.Del("mount")
			query.Del("from")
			request.Out.URL.RawQuery = query.Encode()
			request.Out.Header.Set("Authorization", "Bearer "+auth)
			request.Out.Header.Del("Cookie")
		},
		ModifyResponse: func(response *http.Response) error {
			response.Header.Del("WWW-Authenticate")
			if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
				return errors.New("upstream registry rejected its credentials")
			}
			if location := response.Header.Get("Location"); location != "" {
				parsed, parseErr := url.Parse(location)
				if parseErr != nil || (parsed.Host != "" && parsed.Host != base.Host) ||
					!strings.HasPrefix(parsed.Path, "/v2/"+physical+"/") {
					return errors.New("registry returned an unsupported upload location")
				}
				parsed.Scheme, parsed.Host = "", ""
				parsed.Path = strings.Replace(parsed.Path, "/v2/"+physical+"/", "/v2/"+virtual+"/", 1)
				response.Header.Set("Location", parsed.String())
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "upstream registry is unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

var bearerRealm = regexp.MustCompile(`realm="([^"]+)"`)
var bearerService = regexp.MustCompile(`service="([^"]+)"`)

func (s *RegistryServer) registryBearer(ctx context.Context, base *url.URL, repository string) (string, error) {
	if cached, exists := s.cachedRegistryToken(repository); exists {
		return cached, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := &http.Client{
		Transport:     s.httpClient.Transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.Scheme+"://"+base.Host+"/v2/", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	challenge := response.Header.Get("WWW-Authenticate")
	if closeErr := response.Body.Close(); closeErr != nil {
		return "", closeErr
	}
	realm, service := bearerRealm.FindStringSubmatch(challenge), bearerService.FindStringSubmatch(challenge)
	if len(realm) != 2 || len(service) != 2 {
		return "", errors.New("registry did not supply a bearer challenge")
	}
	authURL, err := url.Parse(realm[1])
	if err != nil {
		return "", err
	}
	gitlab, err := url.Parse(s.gitlabURL)
	if err != nil || authURL.Host != gitlab.Host || authURL.Scheme != gitlab.Scheme {
		return "", errors.New("registry authentication realm differs from configured GitLab")
	}
	username, err := s.registryUsername(ctx, client)
	if err != nil {
		return "", err
	}
	query := authURL.Query()
	query.Set("service", service[1])
	query.Set("scope", "repository:"+repository+":pull,push")
	authURL.RawQuery = query.Encode()
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, authURL.String(), nil)
	if err != nil {
		return "", err
	}
	request.SetBasicAuth(username, s.gitlabPAT)
	response, err = client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("registry token exchange failed")
	}
	var result struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if decodeErr := json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&result); decodeErr != nil {
		return "", decodeErr
	}
	if result.Token == "" {
		result.Token = result.AccessToken
	}
	if result.Token == "" {
		return "", errors.New("registry token exchange returned no token")
	}
	s.cacheRegistryToken(repository, registryToken{Value: result.Token, Expires: time.Now().Add(time.Minute)})
	return result.Token, nil
}

func (s *RegistryServer) registryUsername(ctx context.Context, client *http.Client) (string, error) {
	if cached, exists := s.cachedRegistryToken("username"); exists {
		return cached, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.gitlabURL+"/api/v4/user", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("PRIVATE-TOKEN", s.gitlabPAT)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("registry service identity is unavailable")
	}
	var user struct {
		Username string `json:"username"`
	}
	if decodeErr2 := json.NewDecoder(io.LimitReader(response.Body, 65536)).
		Decode(&user); decodeErr2 != nil ||
		user.Username == "" {
		return "", fmt.Errorf("registry service identity response is invalid")
	}
	s.cacheRegistryToken("username", registryToken{Value: user.Username})
	return user.Username, nil
}

func registryOperationScope(method, operation string) (db.Scope, bool) {
	scope := db.ScopeWrite
	switch method {
	case http.MethodGet:
		return db.ScopeRead, true
	case http.MethodHead:
		return db.ScopeRead, true
	case http.MethodPost:
		return scope, true
	case http.MethodPut:
		return scope, true
	case http.MethodPatch:
		return scope, true
	case http.MethodDelete:
		if !strings.HasPrefix(operation, "blobs/uploads/") {
			scope = db.ScopeAdmin
		}
		return scope, true
	default:
		return scope, false
	}
}

func (s *RegistryServer) cachedRegistryToken(key string) (string, bool) {
	s.proxyMutex.Lock()
	defer s.proxyMutex.Unlock()
	entry, exists := s.proxyTokens[key]
	if !exists {
		return "", false
	}
	if !entry.Expires.IsZero() && time.Now().After(entry.Expires) {
		delete(s.proxyTokens, key)
		return "", false
	}
	return entry.Value, true
}

func (s *RegistryServer) cacheRegistryToken(key string, value registryToken) {
	s.proxyMutex.Lock()
	defer s.proxyMutex.Unlock()
	if len(s.proxyTokens) >= 1024 {
		for candidate := range s.proxyTokens {
			if candidate != "username" {
				delete(s.proxyTokens, candidate)
				break
			}
		}
	}
	s.proxyTokens[key] = value
}
