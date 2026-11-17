package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	registryname "github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

//nolint:gosec // Public development-only identity from dev/registry/htpasswd.
const fixturePushPassword = "loco-dev-builder"

const bundleLayer = "application/vnd.oci.image.layer.v1.tar+gzip"

var errArtifactPublish = errors.New("publish fixture artifact failed")

func fixtureRegistryAuth() authn.Authenticator {
	return authn.FromConfig(authn.AuthConfig{Username: fixtureBuilder, Password: fixturePushPassword})
}

func (f *fixture) publish(ctx context.Context, repository string, data []byte, layerType string) (string, error) {
	image, err := mutate.AppendLayers(empty.Image, static.NewLayer(data, types.MediaType(layerType)))
	if err != nil {
		return "", err
	}
	ref, err := registryname.NewTag(
		strings.TrimPrefix(f.registryURL, "http://") + "/" + repository + ":" + f.version,
	)
	if err != nil {
		return "", err
	}
	if checkErr := remote.Write(
		ref,
		image,
		remote.WithContext(ctx),
		remote.WithAuth(fixtureRegistryAuth()),
	); checkErr != nil {
		return "", errArtifactPublish
	}
	digest, err := image.Digest()
	if err != nil {
		return "", err
	}
	return registryHost + "/" + repository + "@" + digest.String(), nil
}

func bundle(files map[string][]byte) ([]byte, error) {
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for name, data := range files {
		if checkErr := archive.WriteHeader(
			&tar.Header{Name: name, Size: int64(len(data)), Mode: 0600},
		); checkErr != nil {
			return nil, checkErr
		}
		if _, checkErr := archive.Write(data); checkErr != nil {
			return nil, checkErr
		}
	}
	if checkErr := archive.Close(); checkErr != nil {
		return nil, checkErr
	}
	if checkErr := compressed.Close(); checkErr != nil {
		return nil, checkErr
	}
	return output.Bytes(), nil
}
