package registryclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/team-loco/loco/api/pkg/registrytoken"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

var (
	ErrURLInvalid          = errors.New("registry url must be http(s)://host[:port] with no path")
	ErrUsernameMissing     = errors.New("registry username is required")
	ErrPasswordMissing     = errors.New("registry password is required")
	ErrTimeoutNotPositive  = errors.New("registry timeout must be positive")
	ErrTagNotFound         = errors.New("tag not found")
	ErrCredentialsAndToken = errors.New("registry username and password are not used with a token source")
)

type TokenSource func(ctx context.Context, access []registrytoken.Access) (string, error)

type Config struct {
	URL         string
	Username    string
	Password    string
	TokenSource TokenSource
	Timeout     time.Duration
}

func (c Config) Validate() error {
	if err := c.ValidateEndpoint(); err != nil {
		return err
	}
	if c.TokenSource != nil && (c.Username != "" || c.Password != "") {
		return ErrCredentialsAndToken
	}
	if c.TokenSource == nil && c.Username == "" {
		return ErrUsernameMissing
	}
	if c.TokenSource == nil && c.Password == "" {
		return ErrPasswordMissing
	}
	return nil
}

func (c Config) ValidateEndpoint() error {
	if _, err := parseURL(c.URL); err != nil {
		return err
	}
	if c.Timeout <= 0 {
		return ErrTimeoutNotPositive
	}
	return nil
}

func parseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrURLInvalid, raw, err)
	}
	if parsed.Scheme != schemeHTTP && parsed.Scheme != schemeHTTPS {
		return nil, fmt.Errorf("%w: %q", ErrURLInvalid, raw)
	}
	if parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.User != nil {
		return nil, fmt.Errorf("%w: %q", ErrURLInvalid, raw)
	}
	return parsed, nil
}

type Client struct {
	registry  name.Registry
	nameOpts  []name.Option
	auth      authn.Authenticator
	tokens    TokenSource
	transport http.RoundTripper
	timeout   time.Duration
}

func New(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	parsed, err := parseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	var nameOpts []name.Option
	if parsed.Scheme == schemeHTTP {
		nameOpts = append(nameOpts, name.Insecure)
	}
	registry, err := name.NewRegistry(parsed.Host, nameOpts...)
	if err != nil {
		return nil, fmt.Errorf("registry host %q: %w", parsed.Host, err)
	}
	var auth authn.Authenticator
	if cfg.TokenSource == nil {
		auth = authn.FromConfig(authn.AuthConfig{Username: cfg.Username, Password: cfg.Password})
	}
	return &Client{
		registry:  registry,
		nameOpts:  nameOpts,
		auth:      auth,
		tokens:    cfg.TokenSource,
		transport: otelhttp.NewTransport(remote.DefaultTransport),
		timeout:   cfg.Timeout,
	}, nil
}

func (c *Client) options(ctx context.Context, access registrytoken.Access) ([]remote.Option, error) {
	auth := c.auth
	if c.tokens != nil {
		token, err := c.tokens(ctx, []registrytoken.Access{access})
		if err != nil {
			return nil, fmt.Errorf("registry token for %s:%s: %w", access.Type, access.Name, err)
		}
		auth = authn.FromConfig(authn.AuthConfig{RegistryToken: token})
	}
	return []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuth(auth),
		remote.WithTransport(c.transport),
	}, nil
}

func repositoryAccess(path, action string) registrytoken.Access {
	return registrytoken.Access{Type: registrytoken.TypeRepository, Name: path, Actions: []string{action}}
}

func (c *Client) repository(path string) (name.Repository, error) {
	repoName := c.registry.Name() + "/" + path
	repo, err := name.NewRepository(repoName, c.nameOpts...)
	if err != nil {
		return name.Repository{}, fmt.Errorf("repository %q: %w", path, err)
	}
	return repo, nil
}

func isNotFound(err error) bool {
	var transportErr *transport.Error
	return errors.As(err, &transportErr) && transportErr.StatusCode == http.StatusNotFound
}

func (c *Client) DeleteManifest(ctx context.Context, path, digest string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	repo, err := c.repository(path)
	if err != nil {
		return err
	}
	ref := repo.Digest(digest)
	access := repositoryAccess(path, registrytoken.ActionDelete)
	opts, err := c.options(ctx, access)
	if err != nil {
		return err
	}
	if deleteErr := remote.Delete(ref, opts...); deleteErr != nil && !isNotFound(deleteErr) {
		return fmt.Errorf("delete %s: %w", ref, deleteErr)
	}
	return nil
}

func (c *Client) Repositories(ctx context.Context, after string, limit int) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	access := repositoryAccess(registrytoken.CatalogRepository, registrytoken.ActionPull)
	opts, err := c.options(ctx, access)
	if err != nil {
		return nil, err
	}
	repos, err := remote.CatalogPage(c.registry, after, limit, opts...)
	if err != nil {
		return nil, fmt.Errorf("list repositories: %w", err)
	}
	return repos, nil
}

func (c *Client) Tags(ctx context.Context, path string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	repo, err := c.repository(path)
	if err != nil {
		return nil, err
	}
	access := repositoryAccess(path, registrytoken.ActionPull)
	opts, err := c.options(ctx, access)
	if err != nil {
		return nil, err
	}
	tags, err := remote.List(repo, opts...)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list tags of %s: %w", repo, err)
	}
	return tags, nil
}

func (c *Client) TagDigest(ctx context.Context, path, tag string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	repo, err := c.repository(path)
	if err != nil {
		return "", err
	}
	ref := repo.Tag(tag)
	access := repositoryAccess(path, registrytoken.ActionPull)
	opts, err := c.options(ctx, access)
	if err != nil {
		return "", err
	}
	desc, err := remote.Head(ref, opts...)
	if isNotFound(err) {
		return "", ErrTagNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	return desc.Digest.String(), nil
}
