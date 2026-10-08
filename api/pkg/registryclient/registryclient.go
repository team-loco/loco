package registryclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

var (
	ErrURLInvalid         = errors.New("registry url must be http(s)://host[:port] with no path")
	ErrUsernameMissing    = errors.New("registry username is required")
	ErrPasswordMissing    = errors.New("registry password is required")
	ErrTimeoutNotPositive = errors.New("registry timeout must be positive")
)

type Config struct {
	URL      string
	Username string
	Password string
	Timeout  time.Duration
}

func (c Config) Validate() error {
	if _, err := parseURL(c.URL); err != nil {
		return err
	}
	if c.Username == "" {
		return ErrUsernameMissing
	}
	if c.Password == "" {
		return ErrPasswordMissing
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
	auth := authn.FromConfig(authn.AuthConfig{Username: cfg.Username, Password: cfg.Password})
	return &Client{
		registry:  registry,
		nameOpts:  nameOpts,
		auth:      auth,
		transport: remote.DefaultTransport,
		timeout:   cfg.Timeout,
	}, nil
}

func (c *Client) options(ctx context.Context) []remote.Option {
	return []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuth(c.auth),
		remote.WithTransport(c.transport),
	}
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
	opts := c.options(ctx)
	if deleteErr := remote.Delete(ref, opts...); deleteErr != nil && !isNotFound(deleteErr) {
		return fmt.Errorf("delete %s: %w", ref, deleteErr)
	}
	return nil
}

func (c *Client) Repositories(ctx context.Context, after string, limit int) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	opts := c.options(ctx)
	repos, err := remote.CatalogPage(c.registry, after, limit, opts...)
	if err != nil {
		return nil, fmt.Errorf("list repositories: %w", err)
	}
	return repos, nil
}

func (c *Client) ManifestDigests(ctx context.Context, path string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	repo, err := c.repository(path)
	if err != nil {
		return nil, err
	}
	opts := c.options(ctx)
	tags, err := remote.List(repo, opts...)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list tags of %s: %w", repo, err)
	}
	var digests []string
	for _, tag := range tags {
		ref := repo.Tag(tag)
		desc, headErr := remote.Head(ref, opts...)
		if isNotFound(headErr) {
			continue
		}
		if headErr != nil {
			return nil, fmt.Errorf("resolve %s: %w", ref, headErr)
		}
		digest := desc.Digest.String()
		if !slices.Contains(digests, digest) {
			digests = append(digests, digest)
		}
	}
	return digests, nil
}
