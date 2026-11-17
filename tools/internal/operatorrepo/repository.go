// Package operatorrepo prepares generated operator repositories for fixture registries.
package operatorrepo

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	yamlv3 "go.yaml.in/yaml/v3"
	"sigs.k8s.io/yaml"
)

const ociRepositoryKind = "OCIRepository"
const kindKey = "kind"
const nameKey = "name"

type Repository struct{ Path, Cluster, Source string }

func edit(path string, update func(map[string]any) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var value map[string]any
	if err = yaml.Unmarshal(data, &value); err != nil {
		return err
	}
	if err = update(value); err != nil {
		return err
	}
	data, err = yaml.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func Merge(path string, overrides []byte) error {
	var values map[string]any
	if err := yaml.Unmarshal(overrides, &values); err != nil {
		return err
	}
	return edit(path, func(base map[string]any) error { merge(base, values); return nil })
}

func merge(base, overrides map[string]any) {
	for key, value := range overrides {
		nested, ok := value.(map[string]any)
		existing, exists := base[key].(map[string]any)
		if ok && exists {
			merge(existing, nested)
		} else {
			base[key] = value
		}
	}
}

func (repo Repository) Values(release string) string {
	return filepath.Join(repo.Path, "clusters", repo.Cluster, "values", release+".yaml")
}
func (repo Repository) Config(name string) string {
	return filepath.Join(repo.Path, "clusters", repo.Cluster, "config", name)
}

// Configure keeps bundle selection and config wiring in the generated repository.
func (repo Repository) Configure(reference, entry string) error {
	repository, digest, ok := strings.Cut(strings.TrimPrefix(reference, "oci://"), "@")
	if !ok || !strings.HasPrefix(digest, "sha256:") {
		return errors.New("bundle reference requires a digest")
	}
	if err := edit(filepath.Join(repo.Path, "releases", "control-plane.yaml"), func(value map[string]any) error {
		spec, ok := value["spec"].(map[string]any)
		if !ok {
			return errors.New("bundle source has no spec")
		}
		spec["url"] = "oci://" + repository
		spec["ref"] = map[string]string{"digest": digest}
		spec["insecure"] = true
		return nil
	}); err != nil {
		return err
	}
	path := filepath.Join(repo.Path, "bases", "all", "loco.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	decoder := yamlv3.NewDecoder(bytes.NewReader(data))
	for {
		var object map[string]any
		if err = decoder.Decode(&object); errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if object == nil {
			continue
		}
		spec, ok := object["spec"].(map[string]any)
		if !ok {
			return errors.New("reconciliation object has no spec")
		}
		metadata, ok := object["metadata"].(map[string]any)
		if !ok {
			return errors.New("reconciliation object has no metadata")
		}
		if metadata["name"] == "loco" {
			spec["path"] = entry
		} else {
			spec["sourceRef"] = map[string]string{kindKey: ociRepositoryKind, nameKey: repo.Source}
		}
		encoded, marshalErr := yaml.Marshal(object)
		if marshalErr != nil {
			return marshalErr
		}
		output.Write(encoded)
		output.WriteString("---\n")
	}
	return os.WriteFile(path, output.Bytes(), 0600)
}

func (repo Repository) AddResources(resources []byte) error {
	if err := os.WriteFile(repo.Config("fixture-resources.yaml"), resources, 0600); err != nil {
		return err
	}
	return edit(repo.Config("kustomization.yaml"), func(value map[string]any) error {
		resources, ok := value["resources"].([]any)
		if !ok {
			return errors.New("cluster config has no resources")
		}
		value["resources"] = append(resources, "fixture-resources.yaml")
		return nil
	})
}

// Bootstrap points Flux at a generated repository published as an OCI artifact.
func (repo Repository) Bootstrap(reference string) ([]byte, error) {
	repository, pin, ok := strings.Cut(strings.TrimPrefix(reference, "oci://"), "@")
	ref := map[string]string{"digest": pin}
	if !ok {
		repository, ok = strings.CutSuffix(repository, ":local")
		if !ok {
			return nil, errors.New("config reference requires a digest or local tag")
		}
		ref = map[string]string{"tag": "local"}
	}
	source, err := yaml.Marshal(map[string]any{
		"apiVersion": "source.toolkit.fluxcd.io/v1", kindKey: ociRepositoryKind,
		"metadata": map[string]string{nameKey: repo.Source, "namespace": "flux-system"},
		"spec":     map[string]any{"interval": "1m", "url": "oci://" + repository, "ref": ref, "insecure": true},
	})
	if err != nil {
		return nil, err
	}
	root, err := yaml.Marshal(map[string]any{
		"apiVersion": "kustomize.toolkit.fluxcd.io/v1", kindKey: "Kustomization",
		"metadata": map[string]string{nameKey: repo.Source, "namespace": "flux-system"},
		"spec": map[string]any{"interval": "1m", "path": "./clusters/" + repo.Cluster, "prune": true,
			"sourceRef": map[string]string{kindKey: ociRepositoryKind, nameKey: repo.Source}},
	})
	if err != nil {
		return nil, err
	}
	return append(append(source, []byte("---\n")...), root...), nil
}
