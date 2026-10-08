package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"

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
)

var (
	ErrTagExists     = errors.New("tag already exists")
	ErrCacheTooLarge = errors.New("cache is too large")
)

type Client struct {
	insecure bool
	options  []remote.Option
}

func New(ctx context.Context, insecure bool) *Client {
	keychain := remote.WithAuthFromKeychain(authn.DefaultKeychain)
	return &Client{
		insecure: insecure,
		options: []remote.Option{
			remote.WithContext(ctx),
			keychain,
			remote.WithUserAgent(userAgent),
		},
	}
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
	img, err := remote.Image(digest, c.options...)
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

func (c *Client) ensureTagFree(tag name.Tag) error {
	_, err := remote.Head(tag, c.options...)
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
	if err := c.ensureTagFree(tag); err != nil {
		return v1.Hash{}, err
	}
	if len(manifest.Manifests) == 1 && isImage(manifest.Manifests[0].MediaType) {
		img, imgErr := index.Image(manifest.Manifests[0].Digest)
		if imgErr != nil {
			return v1.Hash{}, fmt.Errorf("read image from %s: %w", dir, imgErr)
		}
		if err := remote.Write(tag, img, c.options...); err != nil {
			return v1.Hash{}, fmt.Errorf("push %s: %w", tag, err)
		}
		return img.Digest()
	}
	if imageOnly {
		return v1.Hash{}, fmt.Errorf("%s/index.json must list exactly one image manifest", dir)
	}
	if err := remote.WriteIndex(tag, index, c.options...); err != nil {
		return v1.Hash{}, fmt.Errorf("push %s: %w", tag, err)
	}
	return index.Digest()
}
