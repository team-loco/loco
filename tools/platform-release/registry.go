package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	manifestType      = "application/vnd.oci.image.manifest.v1+json"
	layerType         = "application/vnd.oci.image.layer.v1.tar+gzip"
	helmLayerType     = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	contentAnnotation = "io.loco.platform.content"
	registryTimeout   = 120 * time.Second
)

var repositoryPattern = regexp.MustCompile(`^[a-z0-9-]+/[a-z0-9_.-]+$`)
var challengeFields = regexp.MustCompile(`(\w+)="([^"]+)"`)

type registry struct {
	repository, username, password, host, base string
	client                                     *http.Client
	mu                                         sync.Mutex
	tokens                                     map[string]string
}

func newRegistry(repository, username, password, host string) (*registry, error) {
	if !repositoryPattern.MatchString(repository) {
		return nil, errors.New("repository must be a lowercase owner/repository")
	}
	scheme := "https://"
	if strings.HasPrefix(host, "localhost:") {
		scheme = "http://"
	}
	return &registry{repository: repository, username: username, password: password, host: host, base: scheme + host,
		client: &http.Client{
			Timeout: registryTimeout,
			// Registry upload Locations are validated explicitly; redirects must not forward credentials.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}, tokens: map[string]string{}}, nil
}

func (r *registry) public() *registry {
	return &registry{
		repository: r.repository,
		host:       r.host,
		base:       r.base,
		client:     r.client,
		tokens:     map[string]string{},
	}
}

func (r *registry) request(
	ctx context.Context,
	method, path string,
	data []byte,
	headers http.Header,
	missing bool,
) ([]byte, http.Header, error) {
	parts := strings.SplitN(strings.TrimPrefix(path, "/v2/"), "/manifests/", 2)
	repository, _, _ := strings.Cut(parts[0], "/blobs/")
	actions := "pull,push"
	if method == http.MethodGet || method == http.MethodHead {
		actions = "pull"
	}
	scope := "repository:" + repository + ":" + actions
	requestHeaders := headers.Clone()
	if requestHeaders == nil {
		requestHeaders = http.Header{}
	}
	r.mu.Lock()
	auth := r.tokens[scope]
	r.mu.Unlock()
	if auth != "" {
		requestHeaders.Set("Authorization", auth)
	}
	for attempt := range 2 {
		endpoint, err := registryEndpoint(r.base, path)
		if err != nil {
			return nil, nil, err
		}
		// #nosec G704 -- The operator selects the registry; registryEndpoint confines requests to its origin.
		req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(data))
		if err != nil {
			return nil, nil, err
		}
		req.Header = requestHeaders.Clone()
		// #nosec G704 -- req targets the operator-selected registry validated above.
		response, err := r.client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, nil, readErr
		}
		if closeErr != nil {
			return nil, nil, closeErr
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return body, response.Header, nil
		}
		if response.StatusCode == http.StatusNotFound && missing {
			var payload struct {
				Errors []struct {
					Code string `json:"code"`
				} `json:"errors"`
			}
			if checkErr := json.Unmarshal(body, &payload); checkErr != nil {
				return nil, nil, fmt.Errorf("registry returned an invalid absence response: %w", checkErr)
			}
			if len(payload.Errors) == 0 {
				return nil, nil, errors.New("registry absence is not confirmed")
			}
			for _, item := range payload.Errors {
				if item.Code != "MANIFEST_UNKNOWN" && item.Code != "NAME_UNKNOWN" && item.Code != "BLOB_UNKNOWN" {
					return nil, nil, errors.New("registry absence is not confirmed")
				}
			}
			return nil, http.Header{}, nil
		}
		if response.StatusCode != http.StatusUnauthorized || attempt != 0 {
			return nil, nil, fmt.Errorf("registry %s failed with HTTP %d", method, response.StatusCode)
		}
		auth, err = r.authenticate(ctx, response.Header.Get("WWW-Authenticate"), scope)
		if err != nil {
			return nil, nil, err
		}

		r.mu.Lock()
		r.tokens[scope] = auth
		r.mu.Unlock()
		requestHeaders.Set("Authorization", auth)
	}
	return nil, nil, errors.New("registry authentication failed")
}

func registryEndpoint(base, path string) (*url.URL, error) {
	origin, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	relative, err := url.ParseRequestURI(path)
	if err != nil {
		return nil, err
	}
	if origin.Host == "" || (origin.Scheme != "https" && origin.Scheme != "http") ||
		origin.User != nil || relative.IsAbs() || relative.Host != "" || !strings.HasPrefix(relative.Path, "/v2/") {
		return nil, errors.New("invalid registry endpoint")
	}
	endpoint := origin.ResolveReference(relative)
	if endpoint.Scheme != origin.Scheme || endpoint.Host != origin.Host || !strings.HasPrefix(endpoint.Path, "/v2/") {
		return nil, errors.New("registry endpoint changes origin")
	}
	return endpoint, nil
}

func (r *registry) manifest(ctx context.Context, repository, reference string, missing bool) (string, error) {
	accept := strings.Join([]string{manifestType,
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.docker.distribution.manifest.v2+json"}, ",")
	data, headers, err := r.request(
		ctx,
		http.MethodGet,
		"/v2/"+repository+"/manifests/"+reference,
		nil,
		http.Header{acceptHeader: {accept}},
		missing,
	)
	if err != nil || data == nil {
		return "", err
	}
	result := digest(data)
	if supplied := headers.Get("Docker-Content-Digest"); supplied != "" && supplied != result {
		return "", errors.New("registry manifest digest mismatch")
	}
	return result, nil
}

func (r *registry) requireImagePlatforms(ctx context.Context, repository, reference string) error {
	data, _, err := r.request(
		ctx,
		http.MethodGet,
		"/v2/"+repository+"/manifests/"+reference,
		nil,
		http.Header{
			acceptHeader: {
				"application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json",
			},
		},
		false,
	)
	if err != nil {
		return err
	}
	if digest(data) != reference {
		return errors.New("registry image index digest mismatch")
	}
	var index struct {
		Manifests []struct {
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if checkErr := json.Unmarshal(data, &index); checkErr != nil {
		return checkErr
	}
	for _, arch := range []string{archAMD64, archARM64} {
		matches := 0
		for _, entry := range index.Manifests {
			if entry.Platform.OS == linuxOS && entry.Platform.Architecture == arch {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("platform image must contain exactly one linux/%s manifest", arch)
		}
	}
	return nil
}

func (r *registry) assertAbsent(ctx context.Context, repository, version string) error {
	found, err := r.manifest(ctx, repository, version, true)
	if err != nil {
		return err
	}
	if found != "" {
		return fmt.Errorf("immutable release already exists: %s:%s", repository, version)
	}
	return nil
}

func (r *registry) blob(ctx context.Context, repository string, data []byte) (string, error) {
	_, headers, err := r.request(ctx, http.MethodPost, "/v2/"+repository+"/blobs/uploads/", []byte{}, nil, false)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(r.base)
	if err != nil {
		return "", err
	}
	location, err := url.Parse(headers.Get("Location"))
	if err != nil {
		return "", err
	}
	resolved := base.ResolveReference(location)
	if resolved.Scheme != base.Scheme || resolved.Host != base.Host {
		return "", errors.New("unexpected registry upload location")
	}
	value := digest(data)
	query := resolved.Query()
	query.Add(fieldDigest, value)
	resolved.RawQuery = query.Encode()
	_, _, err = r.request(
		ctx,
		http.MethodPut,
		resolved.RequestURI(),
		data,
		http.Header{"Content-Type": {"application/octet-stream"}},
		false,
	)
	return value, err
}

func (r *registry) publish(
	ctx context.Context,
	repository, version string,
	data []byte,
	config any,
	mediaType string,
	replace bool,
) (string, error) {
	if !replace {
		if checkErr := r.assertAbsent(ctx, repository, version); checkErr != nil {
			return "", checkErr
		}
	}
	configData := encoded(config)
	configDigest, err := r.blob(ctx, repository, configData)
	if err != nil {
		return "", err
	}
	layerDigest, err := r.blob(ctx, repository, data)
	if err != nil {
		return "", err
	}
	annotation, err := content(data)
	if err != nil {
		return "", err
	}
	configType := "application/vnd.oci.image.config.v1+json"
	if mediaType == helmLayerType {
		configType = "application/vnd.cncf.helm.config.v1+json"
	}
	body := encoded(map[string]any{
		"schemaVersion": 2,
		fieldMediaType:  manifestType,
		"config":        map[string]any{fieldMediaType: configType, fieldDigest: configDigest, "size": len(configData)},
		"layers":        []any{map[string]any{fieldMediaType: mediaType, fieldDigest: layerDigest, "size": len(data)}},
		"annotations": map[string]string{
			"org.opencontainers.image.source":  "https://github.com/" + r.repository,
			"org.opencontainers.image.version": version,
			contentAnnotation:                  annotation,
		},
	})
	if !replace {
		if checkErr := r.assertAbsent(ctx, repository, version); checkErr != nil {
			return "", checkErr
		}
	}
	if _, _, checkErr := r.request(
		ctx,
		http.MethodPut,
		"/v2/"+repository+"/manifests/"+version,
		body,
		http.Header{"Content-Type": {manifestType}},
		false,
	); checkErr != nil {
		return "", checkErr
	}
	result, err := r.manifest(ctx, repository, version, false)
	if err != nil {
		return "", err
	}
	if result != digest(body) {
		return "", errors.New("published artifact digest mismatch")
	}
	return result, nil
}

func (r *registry) ensure(
	ctx context.Context,
	repository, version string,
	data []byte,
	config any,
	mediaType string,
) (string, error) {
	reference, err := r.manifest(ctx, repository, version, true)
	if err != nil {
		return "", err
	}
	if reference == "" {
		return r.publish(ctx, repository, version, data, config, mediaType, false)
	}
	body, _, err := r.request(
		ctx,
		http.MethodGet,
		"/v2/"+repository+"/manifests/"+reference,
		nil,
		http.Header{acceptHeader: {manifestType}},
		false,
	)
	if err != nil {
		return "", err
	}
	if digest(body) != reference {
		return "", errors.New("registry manifest digest mismatch")
	}
	var manifest struct {
		Annotations map[string]string `json:"annotations"`
		Layers      []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if checkErr := json.Unmarshal(body, &manifest); checkErr != nil {
		return "", checkErr
	}
	annotation, err := content(data)
	if err != nil {
		return "", err
	}
	if manifest.Annotations[contentAnnotation] == annotation {
		return reference, nil
	}
	return "", fmt.Errorf("%s:%s is already published with different content; bump its version", repository, version)
}

func (r *registry) authenticate(ctx context.Context, challenge, scope string) (string, error) {
	var auth string
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte(r.username+":"+r.password))
	if strings.HasPrefix(challenge, "Basic ") && r.password != "" {
		auth = basic
	} else {
		parameters := map[string]string{}
		for _, match := range challengeFields.FindAllStringSubmatch(challenge, -1) {
			parameters[match[1]] = match[2]
		}
		if parameters["realm"] != r.base+"/token" || parameters["service"] != r.host {
			return "", errors.New("unexpected registry authentication challenge")
		}
		query := url.Values{"service": {r.host}, "scope": {scope}}
		tokenRequest, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			parameters["realm"]+"?"+query.Encode(),
			nil,
		)
		if err != nil {
			return "", err
		}
		if r.password != "" {
			tokenRequest.Header.Set("Authorization", basic)
		}
		tokenResponse, err := r.client.Do(tokenRequest)
		if err != nil {
			return "", err
		}
		var token struct {
			Token string `json:"token"`
		}
		err = json.NewDecoder(tokenResponse.Body).Decode(&token)
		closeErr := tokenResponse.Body.Close()
		if tokenResponse.StatusCode != http.StatusOK {
			return "", fmt.Errorf("registry token request failed with HTTP %d", tokenResponse.StatusCode)
		}
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		if token.Token == "" {
			return "", errors.New("registry token response contains no token")
		}
		auth = "Bearer " + token.Token
	}
	return auth, nil
}
