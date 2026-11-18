// Command imagecache keeps the Docker Hub images CI pulls in an OCI layout and serves the layout
// as a Docker Hub mirror.
//
//	imagecache images            print the images
//	imagecache save DIR          write the host platform's image of each, and its index, to DIR
//	imagecache serve DIR ADDR    serve DIR's manifests and blobs at ADDR
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"sigs.k8s.io/yaml"
)

const refAnnotation = "org.opencontainers.image.ref.name"

var errUsage = errors.New("usage: imagecache images | save DIR | serve DIR ADDR")

// images lists the Docker Hub images of the compose files, chart values and e2e fixture
// Dockerfiles CI runs, in the repository root.
func images(root fs.FS) ([]string, error) {
	var refs []string
	for _, file := range []string{"dev/compose.yaml", ".github/compose.api-tests.yaml"} {
		var compose struct {
			Services map[string]struct {
				Image string `json:"image"`
			} `json:"services"`
		}
		if err := readYAML(root, file, &compose); err != nil {
			return nil, err
		}
		for _, service := range compose.Services {
			refs = append(refs, service.Image)
		}
	}
	type image struct {
		Repository string `json:"repository"`
		Tag        string `json:"tag"`
	}
	var controlPlane struct {
		ClickHouse struct {
			ClickHouse struct {
				Image image `json:"image"`
			} `json:"clickhouse"`
		} `json:"clickhouse"`
	}
	if err := readYAML(root, "charts/loco-control-plane/values.yaml", &controlPlane); err != nil {
		return nil, err
	}
	var worker struct {
		Builds struct {
			BuildkitImage image `json:"buildkitImage"`
		} `json:"builds"`
		Collector struct {
			InitContainers []struct {
				Image string `json:"image"`
			} `json:"initContainers"`
		} `json:"otel-col-daemon"`
	}
	if err := readYAML(root, "charts/loco-worker/values.yaml", &worker); err != nil {
		return nil, err
	}
	clickhouse, buildkit := controlPlane.ClickHouse.ClickHouse.Image, worker.Builds.BuildkitImage
	refs = append(refs, clickhouse.Repository+":"+clickhouse.Tag, buildkit.Repository+":"+buildkit.Tag)
	for _, container := range worker.Collector.InitContainers {
		refs = append(refs, container.Image)
	}
	err := fs.WalkDir(root, "tests/e2e/fixtures", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), "Dockerfile") {
			return err
		}
		from, err := fromImages(root, path)
		refs = append(refs, from...)
		return err
	})
	if err != nil {
		return nil, err
	}
	var hub []string
	for _, ref := range refs {
		parsed, err := name.ParseReference(ref)
		if err != nil {
			continue // a compose variable or a build stage
		}
		if parsed.Context().RegistryStr() == name.DefaultRegistry {
			hub = append(hub, ref)
		}
	}
	slices.Sort(hub)
	return slices.Compact(hub), nil
}

func readYAML(root fs.FS, path string, into any) error {
	data, err := fs.ReadFile(root, path)
	if err != nil {
		return err
	}
	if err = yaml.Unmarshal(data, into); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func fromImages(root fs.FS, dockerfile string) ([]string, error) {
	data, err := fs.ReadFile(root, dockerfile)
	if err != nil {
		return nil, err
	}
	var from []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "FROM" {
			continue
		}
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "--") {
				from = append(from, field)
				break
			}
		}
	}
	return from, scanner.Err()
}

// save writes platform's image of each ref to an OCI layout at dir, with the index the ref names
// when it names one, each annotated with the ref.
func save(ctx context.Context, dir string, platform v1.Platform, refs []string) error {
	path, err := layout.Write(dir, empty.Index)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		parsed, err := name.ParseReference(ref)
		if err != nil {
			return err
		}
		descriptor, err := remote.Get(parsed, remote.WithContext(ctx), remote.WithPlatform(platform))
		if err != nil {
			return fmt.Errorf("save %s: %w", ref, err)
		}
		annotations := map[string]string{refAnnotation: parsed.Name()}
		if descriptor.MediaType.IsIndex() {
			if err = path.WriteBlob(descriptor.Digest, io.NopCloser(bytes.NewReader(descriptor.Manifest))); err != nil {
				return err
			}
			index := descriptor.Descriptor
			index.Annotations = annotations
			if err = path.AppendDescriptor(index); err != nil {
				return err
			}
		}
		image, err := descriptor.Image()
		if err != nil {
			return fmt.Errorf("save %s: %w", ref, err)
		}
		if err = path.AppendImage(image, layout.WithAnnotations(annotations)); err != nil {
			return fmt.Errorf("save %s: %w", ref, err)
		}
	}
	return nil
}

// mirror serves a layout's manifests and blobs under any repository, and resolves the tags its
// refs name in their repository.
type mirror struct {
	path  layout.Path
	media map[v1.Hash]types.MediaType
	tags  map[string]v1.Hash
}

func newMirror(dir string) (*mirror, error) {
	path, err := layout.FromPath(dir)
	if err != nil {
		return nil, err
	}
	index, err := path.ImageIndex()
	if err != nil {
		return nil, err
	}
	manifest, err := index.IndexManifest()
	if err != nil {
		return nil, err
	}
	m := &mirror{path: path, media: map[v1.Hash]types.MediaType{}, tags: map[string]v1.Hash{}}
	for _, descriptor := range manifest.Manifests {
		m.media[descriptor.Digest] = descriptor.MediaType
		if tag, err := name.NewTag(descriptor.Annotations[refAnnotation], name.Insecure); err == nil {
			key := tag.RepositoryStr() + ":" + tag.TagStr()
			if _, seen := m.tags[key]; !seen {
				m.tags[key] = descriptor.Digest
			}
		}
	}
	return m, nil
}

func (m *mirror) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	status := m.serve(w, r)
	log.Printf("%s %s %d", r.Method, r.URL.Path, status)
}

func (m *mirror) serve(w http.ResponseWriter, r *http.Request) int {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "read only", http.StatusMethodNotAllowed)
		return http.StatusMethodNotAllowed
	}
	if r.URL.Path == "/v2/" {
		return http.StatusOK
	}
	repository, reference, isManifest := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v2/"), "/manifests/")
	if !isManifest {
		var isBlob bool
		if _, reference, isBlob = strings.Cut(r.URL.Path, "/blobs/"); !isBlob {
			http.NotFound(w, r)
			return http.StatusNotFound
		}
	}
	digest, err := v1.NewHash(reference)
	if err != nil && isManifest {
		var tagged bool
		if digest, tagged = m.tags[repository+":"+reference]; tagged {
			err = nil
		}
	}
	media, known := m.media[digest]
	if err != nil || (isManifest && !known) {
		http.NotFound(w, r)
		return http.StatusNotFound
	}
	blob, err := os.Open(filepath.Join(string(m.path), "blobs", digest.Algorithm, digest.Hex))
	if err != nil {
		http.NotFound(w, r)
		return http.StatusNotFound
	}
	defer blob.Close()
	if !isManifest {
		media = "application/octet-stream"
	}
	w.Header().Set("Content-Type", string(media))
	w.Header().Set("Docker-Content-Digest", digest.String())
	http.ServeContent(w, r, "", time.Time{}, blob)
	return http.StatusOK
}

func run(ctx context.Context, args []string) error {
	switch {
	case len(args) == 1 && args[0] == "images":
		refs, err := images(os.DirFS("."))
		if err != nil {
			return err
		}
		fmt.Println(strings.Join(refs, "\n"))
		return nil
	case len(args) == 2 && args[0] == "save":
		refs, err := images(os.DirFS("."))
		if err != nil {
			return err
		}
		return save(ctx, args[1], v1.Platform{OS: "linux", Architecture: runtime.GOARCH}, refs)
	case len(args) == 3 && args[0] == "serve":
		m, err := newMirror(args[1])
		if err != nil {
			return err
		}
		server := &http.Server{Addr: args[2], Handler: m, ReadHeaderTimeout: time.Minute}
		return server.ListenAndServe()
	}
	return errUsage
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		log.Fatal(err)
	}
}
