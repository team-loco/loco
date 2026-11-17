package imageresolver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	requiredOS           = "linux"
	requiredArchitecture = "amd64"
)

var ErrPlatformUnsupported = errors.New("image has no " + requiredOS + "/" + requiredArchitecture + " variant")

type Resolver struct {
	timeout   time.Duration
	maxBytes  int64
	transport http.RoundTripper
}

func New(timeout time.Duration, maxBytes int64) *Resolver {
	transport := otelhttp.NewTransport(remote.DefaultTransport)
	return &Resolver{timeout: timeout, transport: transport, maxBytes: maxBytes}
}

func (r *Resolver) Resolve(ctx context.Context, ref name.Reference) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	auth := remote.WithAuth(authn.Anonymous)
	withCtx := remote.WithContext(ctx)
	withTransport := remote.WithTransport(r.transport)
	desc, err := remote.Get(ref, auth, withCtx, withTransport)
	if err != nil {
		return "", fmt.Errorf("fetch manifest for %s: %w", ref, err)
	}

	var img v1.Image
	switch {
	case desc.MediaType.IsIndex():
		img, err = indexImage(desc)
	case desc.MediaType.IsImage():
		img, err = desc.Image()
	default:
		return "", fmt.Errorf("unsupported manifest media type %s", desc.MediaType)
	}
	if err != nil {
		return "", err
	}
	if err := checkSize(img, r.maxBytes); err != nil {
		return "", err
	}
	if err := checkImage(img); err != nil {
		return "", err
	}
	return desc.Digest.String(), nil
}

// SizeLimitError contains only byte counts, safe to expose to API callers.
type SizeLimitError struct{ Size, Limit int64 }

func (e *SizeLimitError) Error() string {
	return fmt.Sprintf("image size %d bytes exceeds limit %d bytes", e.Size, e.Limit)
}

func checkSize(img v1.Image, limit int64) error {
	manifest, err := img.Manifest()
	if err != nil {
		return fmt.Errorf("read image manifest: %w", err)
	}
	sizes := make(map[v1.Hash]int64)
	var total int64
	for _, blob := range slices.Concat([]v1.Descriptor{manifest.Config}, manifest.Layers) {
		// A compressed layer or image config cannot be an empty blob. This also
		// rejects omitted and null size fields, which decode as zero.
		if blob.Size <= 0 {
			return fmt.Errorf("invalid size %d for blob %s", blob.Size, blob.Digest)
		}
		if size, ok := sizes[blob.Digest]; ok {
			if size != blob.Size {
				return fmt.Errorf("conflicting sizes for blob %s", blob.Digest)
			}
			continue
		}
		sizes[blob.Digest] = blob.Size
		if blob.Size > math.MaxInt64-total {
			return errors.New("image size overflow")
		}
		total += blob.Size
	}
	if total > limit {
		return &SizeLimitError{Size: total, Limit: limit}
	}
	return nil
}

func indexImage(desc *remote.Descriptor) (v1.Image, error) {
	index, err := desc.ImageIndex()
	if err != nil {
		return nil, fmt.Errorf("read image index: %w", err)
	}
	manifest, err := index.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("read index manifest: %w", err)
	}
	for _, m := range manifest.Manifests {
		if m.Platform == nil {
			continue
		}
		if m.Platform.OS == requiredOS && m.Platform.Architecture == requiredArchitecture {
			return index.Image(m.Digest)
		}
	}
	return nil, ErrPlatformUnsupported
}

func checkImage(img v1.Image) error {
	cfg, err := img.ConfigFile()
	if err != nil {
		return fmt.Errorf("read image config: %w", err)
	}
	if cfg.OS == requiredOS && cfg.Architecture == requiredArchitecture {
		return nil
	}
	return ErrPlatformUnsupported
}
