package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

const (
	archAMD64 = "amd64"
	archARM64 = "arm64"
	linuxOS   = "linux"
)

const (
	manifestType    = "application/vnd.oci.image.manifest.v1+json"
	layerType       = "application/vnd.oci.image.layer.v1.tar+gzip"
	helmLayerType   = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	registryTimeout = 120 * time.Second
)

var repositoryPattern = regexp.MustCompile(`^[a-z0-9-]+/[a-z0-9_.-]+$`)

type registry struct {
	repository, username, password, host string
	transport                            http.RoundTripper
}

func newRegistry(repository, username, password, host string) (*registry, error) {
	if !repositoryPattern.MatchString(repository) {
		return nil, errors.New("repository must be a lowercase owner/repository")
	}
	base := &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: registryTimeout}
	return &registry{repository: repository, username: username, password: password, host: host, transport: base}, nil
}

func (r *registry) public() *registry {
	return &registry{repository: r.repository, host: r.host, transport: r.transport}
}

func (r *registry) options(ctx context.Context) []remote.Option {
	auth := authn.Anonymous
	if r.password != "" {
		auth = &authn.Basic{Username: r.username, Password: r.password}
	}
	return []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuth(auth),
		remote.WithTransport(confinedTransport{host: r.host, next: r.transport}),
	}
}

// confinedTransport keeps requests on the registry, except one credential-free redirect of a blob download.
type confinedTransport struct {
	host string
	next http.RoundTripper
}

func (t confinedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Response == nil {
		if req.URL.Host != t.host {
			return nil, fmt.Errorf("unexpected registry location %s", req.URL.Host)
		}
		return t.next.RoundTrip(req)
	}
	from := req.Response.Request
	if from.Response != nil || from.Method != http.MethodGet || !strings.Contains(from.URL.Path, "/blobs/") {
		return nil, fmt.Errorf(
			"registry %s %s redirected with HTTP %d",
			from.Method,
			from.URL.Path,
			req.Response.StatusCode,
		)
	}
	req = req.Clone(req.Context())
	req.Header.Del("Authorization")
	return t.next.RoundTrip(req)
}

func (r *registry) reference(repository, reference string) (name.Reference, error) {
	separator := ":"
	if strings.HasPrefix(reference, "sha256:") {
		separator = "@"
	}
	return name.ParseReference(r.host + "/" + repository + separator + reference)
}

func (r *registry) get(ctx context.Context, repository, reference string) (*remote.Descriptor, error) {
	ref, err := r.reference(repository, reference)
	if err != nil {
		return nil, err
	}
	return remote.Get(ref, r.options(ctx)...)
}

// manifest returns the digest of a manifest, or "" when missing allows a registry-confirmed absence.
func (r *registry) manifest(ctx context.Context, repository, reference string, missing bool) (string, error) {
	descriptor, err := r.get(ctx, repository, reference)
	if err != nil {
		if missing && absent(err) {
			return "", nil
		}
		return "", err
	}
	return descriptor.Digest.String(), nil
}

func absent(err error) bool {
	var failure *transport.Error
	if !errors.As(err, &failure) || failure.StatusCode != http.StatusNotFound || len(failure.Errors) == 0 {
		return false
	}
	for _, diagnostic := range failure.Errors {
		switch diagnostic.Code {
		case transport.ManifestUnknownErrorCode, transport.NameUnknownErrorCode, transport.BlobUnknownErrorCode:
		default:
			return false
		}
	}
	return true
}

func (r *registry) requireImagePlatforms(ctx context.Context, repository, reference string) error {
	descriptor, err := r.get(ctx, repository, reference)
	if err != nil {
		return err
	}
	index, err := v1.ParseIndexManifest(bytes.NewReader(descriptor.Manifest))
	if err != nil {
		return err
	}
	for _, arch := range []string{archAMD64, archARM64} {
		matches := 0
		for _, entry := range index.Manifests {
			if entry.Platform != nil && entry.Platform.OS == linuxOS && entry.Platform.Architecture == arch {
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

// rawManifest is a manifest published byte for byte.
type rawManifest struct{ data []byte }

func (m rawManifest) RawManifest() ([]byte, error) { return m.data, nil }

func (rawManifest) MediaType() (types.MediaType, error) { return manifestType, nil }

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
	target, err := name.NewRepository(r.host + "/" + repository)
	if err != nil {
		return "", err
	}
	configType := "application/vnd.oci.image.config.v1+json"
	if mediaType == helmLayerType {
		configType = "application/vnd.cncf.helm.config.v1+json"
	}
	configData := encoded(config)
	for _, blob := range []v1.Layer{
		static.NewLayer(configData, types.MediaType(configType)),
		static.NewLayer(data, types.MediaType(mediaType)),
	} {
		if err = remote.WriteLayer(target, blob, r.options(ctx)...); err != nil {
			return "", err
		}
	}
	body := encoded(map[string]any{
		"schemaVersion": 2,
		fieldMediaType:  manifestType,
		"config": map[string]any{
			fieldMediaType: configType,
			fieldDigest:    digest(configData),
			"size":         len(configData),
		},
		"layers": []any{map[string]any{fieldMediaType: mediaType, fieldDigest: digest(data), "size": len(data)}},
		"annotations": map[string]string{
			"org.opencontainers.image.source":  "https://github.com/" + r.repository,
			"org.opencontainers.image.version": version,
		},
	})
	if !replace {
		if checkErr := r.assertAbsent(ctx, repository, version); checkErr != nil {
			return "", checkErr
		}
	}
	if err = remote.Put(target.Tag(version), rawManifest{body}, r.options(ctx)...); err != nil {
		return "", err
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
	descriptor, err := r.get(ctx, repository, version)
	if err != nil {
		if absent(err) {
			return r.publish(ctx, repository, version, data, config, mediaType, false)
		}
		return "", err
	}
	image, err := descriptor.Image()
	if err != nil {
		return "", err
	}
	layers, err := image.Layers()
	if err != nil {
		return "", err
	}
	if len(layers) != 1 {
		return "", fmt.Errorf("%s:%s must have exactly one layer", repository, version)
	}
	reader, err := layers[0].Compressed()
	if err != nil {
		return "", err
	}
	published, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return "", err
	}
	changed, err := changedFiles(published, data)
	if err != nil {
		return "", err
	}
	if len(changed) != 0 {
		return "", fmt.Errorf(
			"%s:%s is already published with different content; bump its version; differing files: %s",
			repository,
			version,
			strings.Join(changed, ", "),
		)
	}
	return descriptor.Digest.String(), nil
}
