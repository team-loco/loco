package imageresolver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const (
	requiredOS           = "linux"
	requiredArchitecture = "amd64"
)

var ErrPlatformUnsupported = errors.New("image has no linux/amd64 variant")

type Resolver struct {
	timeout time.Duration
}

func New(timeout time.Duration) *Resolver {
	return &Resolver{timeout: timeout}
}

func (r *Resolver) Resolve(ctx context.Context, ref name.Reference) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	auth := remote.WithAuth(authn.Anonymous)
	withCtx := remote.WithContext(ctx)
	desc, err := remote.Get(ref, auth, withCtx)
	if err != nil {
		return "", fmt.Errorf("fetch manifest for %s: %w", ref, err)
	}

	if desc.MediaType.IsIndex() {
		if indexErr := checkIndex(desc); indexErr != nil {
			return "", indexErr
		}
		return desc.Digest.String(), nil
	}

	if desc.MediaType.IsImage() {
		if imageErr := checkImage(desc); imageErr != nil {
			return "", imageErr
		}
		return desc.Digest.String(), nil
	}

	return "", fmt.Errorf("unsupported manifest media type %s", desc.MediaType)
}

func checkIndex(desc *remote.Descriptor) error {
	index, err := desc.ImageIndex()
	if err != nil {
		return fmt.Errorf("read image index: %w", err)
	}
	manifest, err := index.IndexManifest()
	if err != nil {
		return fmt.Errorf("read index manifest: %w", err)
	}
	for _, m := range manifest.Manifests {
		if m.Platform == nil {
			continue
		}
		if m.Platform.OS == requiredOS && m.Platform.Architecture == requiredArchitecture {
			return nil
		}
	}
	return ErrPlatformUnsupported
}

func checkImage(desc *remote.Descriptor) error {
	img, err := desc.Image()
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return fmt.Errorf("read image config: %w", err)
	}
	if cfg.OS == requiredOS && cfg.Architecture == requiredArchitecture {
		return nil
	}
	return ErrPlatformUnsupported
}
