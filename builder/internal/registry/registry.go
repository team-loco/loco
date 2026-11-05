package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

const (
	userAgent     = "loco-builder"
	annotationRef = "org.opencontainers.image.ref.name"
	cacheRefName  = "latest"
	bearerScheme  = "bearer"
)

var transientStatusCodes = []int{
	http.StatusRequestTimeout,
	http.StatusTooManyRequests,
	http.StatusInternalServerError,
	http.StatusBadGateway,
	http.StatusServiceUnavailable,
	http.StatusGatewayTimeout,
}

var (
	ErrTagExists     = errors.New("tag already exists")
	ErrCacheTooLarge = errors.New("cache is too large")
)

type Client struct {
	ctx      context.Context
	insecure bool
	keychain authn.Keychain
	base     http.RoundTripper
	ping     http.RoundTripper
	exchange http.RoundTripper
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
	options  []remote.Option
}

func New(ctx context.Context, insecure bool) *Client {
	retryCodes := transport.WithRetryStatusCodes(transientStatusCodes...)
	retrying := transport.NewRetry(remote.DefaultTransport, retryCodes)
	return &Client{
		ctx:      ctx,
		insecure: insecure,
		keychain: authn.DefaultKeychain,
		base:     remote.DefaultTransport,
		ping:     transport.NewUserAgent(retrying, userAgent),
		exchange: transport.NewUserAgent(remote.DefaultTransport, userAgent),
		now:      time.Now,
		sleep:    sleepContext,
		options: []remote.Option{
			remote.WithContext(ctx),
			remote.WithUserAgent(userAgent),
		},
	}
}

func (c *Client) remoteOptions(repo name.Repository, action string) ([]remote.Option, error) {
	challenge, err := transport.Ping(c.ctx, repo.Registry, c.ping)
	if err != nil {
		return nil, fmt.Errorf("ping %s: %w", repo.Registry, err)
	}
	options := slices.Clone(c.options)
	if !strings.EqualFold(challenge.Scheme, bearerScheme) {
		keychain := remote.WithAuthFromKeychain(c.keychain)
		return append(options, keychain), nil
	}
	auth, err := c.keychain.Resolve(repo.Registry)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials for %s: %w", repo.Registry, err)
	}
	tokens := &tokenTransport{
		inner:     c.base,
		exchange:  c.exchange,
		registry:  repo.Registry,
		challenge: challenge,
		auth:      auth,
		scopes:    []string{repo.Scope(action)},
		now:       c.now,
		sleep:     c.sleep,
	}
	return append(options, remote.WithTransport(tokens)), nil
}

func (c *Client) nameOptions() []name.Option {
	if c.insecure {
		return []name.Option{name.StrictValidation, name.Insecure}
	}
	return []name.Option{name.StrictValidation}
}

func (c *Client) RestoreCache(ref, dir string, maxBytes int64) error {
	digest, err := name.NewDigest(ref, c.nameOptions()...)
	if err != nil {
		return fmt.Errorf("parse cache reference %q: %w", ref, err)
	}
	options, err := c.remoteOptions(digest.Context(), transport.PullScope)
	if err != nil {
		return err
	}
	img, err := remote.Image(digest, options...)
	if err != nil {
		return fmt.Errorf("fetch cache %s: %w", ref, err)
	}
	manifest, err := img.Manifest()
	if err != nil {
		return fmt.Errorf("read cache manifest: %w", err)
	}
	total := manifest.Config.Size
	for _, layer := range manifest.Layers {
		total += layer.Size
	}
	if maxBytes > 0 && total > maxBytes {
		return fmt.Errorf("cache is %d bytes, over the %d byte limit: %w", total, maxBytes, ErrCacheTooLarge)
	}
	path, err := layout.Write(dir, empty.Index)
	if err != nil {
		return fmt.Errorf("create cache layout: %w", err)
	}
	annotations := map[string]string{annotationRef: cacheRefName}
	if err := path.AppendImage(img, layout.WithAnnotations(annotations)); err != nil {
		return fmt.Errorf("write cache layout: %w", err)
	}
	return nil
}

func CredentialRejected(err error) bool {
	terr, ok := errors.AsType[*transport.Error](err)
	return ok && (terr.StatusCode == http.StatusUnauthorized || terr.StatusCode == http.StatusForbidden)
}

func ensureTagFree(tag name.Tag, options []remote.Option) error {
	_, err := remote.Head(tag, options...)
	if err == nil {
		return fmt.Errorf("%s: %w", tag, ErrTagExists)
	}
	var terr *transport.Error
	if errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("check %s: %w", tag, err)
}

func isImage(mediaType types.MediaType) bool {
	return mediaType == types.OCIManifestSchema1 || mediaType == types.DockerManifestSchema2
}

func (c *Client) PushLayout(dir, ref string, imageOnly bool) (v1.Hash, error) {
	tag, err := name.NewTag(ref, c.nameOptions()...)
	if err != nil {
		return v1.Hash{}, fmt.Errorf("parse reference %q: %w", ref, err)
	}
	index, err := layout.ImageIndexFromPath(dir)
	if err != nil {
		return v1.Hash{}, fmt.Errorf("read layout %s: %w", dir, err)
	}
	manifest, err := index.IndexManifest()
	if err != nil {
		return v1.Hash{}, fmt.Errorf("read %s/index.json: %w", dir, err)
	}
	options, err := c.remoteOptions(tag.Context(), transport.PushScope)
	if err != nil {
		return v1.Hash{}, err
	}
	if err := ensureTagFree(tag, options); err != nil {
		return v1.Hash{}, err
	}
	if len(manifest.Manifests) == 1 && isImage(manifest.Manifests[0].MediaType) {
		img, imgErr := index.Image(manifest.Manifests[0].Digest)
		if imgErr != nil {
			return v1.Hash{}, fmt.Errorf("read image from %s: %w", dir, imgErr)
		}
		if err := remote.Write(tag, img, options...); err != nil {
			return v1.Hash{}, fmt.Errorf("push %s: %w", tag, err)
		}
		return img.Digest()
	}
	if imageOnly {
		return v1.Hash{}, fmt.Errorf("%s/index.json must list exactly one image manifest", dir)
	}
	if err := remote.WriteIndex(tag, index, options...); err != nil {
		return v1.Hash{}, fmt.Errorf("push %s: %w", tag, err)
	}
	return index.Digest()
}
