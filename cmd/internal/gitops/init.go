package gitops

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	yamlv3 "go.yaml.in/yaml/v3"

	"github.com/team-loco/loco/cmd/internal/dotenv"
	example "github.com/team-loco/loco/examples/gitops"
	"sigs.k8s.io/yaml"
)

type InitOptions struct {
	Output, API, Web, Domain, Region, Admin, Bundle, APIEnvFile string
	SingleCluster                                               bool
	Name                                                        string
}

func Init(options InitOptions) error {
	if options.SingleCluster &&
		(len(options.Name) > 63 || !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`).MatchString(options.Name)) {
		return errors.New("name must be a lowercase cluster name")
	}
	endpoints := make([]*url.URL, 0, 2)
	for _, address := range []string{options.API, options.Web} {
		endpoint, err := url.Parse(address)
		if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
			endpoint.User != nil ||
			endpoint.RawQuery != "" ||
			endpoint.Fragment != "" {
			return errors.New("api and web must be absolute HTTP URLs without credentials, queries or fragments")
		}
		endpoints = append(endpoints, endpoint)
	}
	if options.Output == "" || options.Domain == "" || options.Region == "" || options.Admin == "" {
		return errors.New("output, domain, region and admin are required")
	}
	repository, pin, ok := strings.Cut(options.Bundle, "@sha256:")
	_, pinErr := hex.DecodeString(pin)
	if !ok || !strings.HasPrefix(repository, "oci://") || len(pin) != 64 || pinErr != nil {
		return errors.New("bundle must be an OCI reference pinned by SHA-256 digest")
	}
	envData, err := os.ReadFile(options.APIEnvFile)
	if err != nil {
		return err
	}
	env, err := dotenv.Parse(bytes.NewReader(envData))
	if err != nil {
		return err
	}
	if err := os.Mkdir(options.Output, 0700); err != nil {
		return err
	}
	apiURL, webURL := endpoints[0], endpoints[1]
	replacements := strings.NewReplacer(
		"https://api.example.com",
		strings.TrimRight(options.API, "/"),
		"https://app.example.com",
		strings.TrimRight(options.Web, "/"),
		"api.example.com",
		apiURL.Hostname(),
		"app.example.com",
		webURL.Hostname(),
		"apps.example.com",
		options.Domain,
		"example-east",
		options.Region,
		"admin@example.com",
		options.Admin,
	)
	if err := copyExample(options, repository, pin, replacements); err != nil {
		return err
	}

	seal := make([]byte, 32)
	if _, err := rand.Read(seal); err != nil {
		return err
	}
	for _, secret := range []struct {
		path, name string
		data       map[string]string
	}{
		{
			"control-plane/config/openbao-seal.yaml", "openbao-seal",
			map[string]string{"key": base64.StdEncoding.EncodeToString(seal)},
		},
		{"control-plane/config/api-env.yaml", "loco-api-env", env},
	} {
		data, marshalErr := Secret("loco-system", secret.name, secret.data)
		if marshalErr != nil {
			return marshalErr
		}
		if writeErr := os.WriteFile(
			filepath.Join(options.Output, "clusters", secret.path),
			data,
			0600,
		); writeErr != nil {
			return writeErr
		}
	}
	if options.SingleCluster {
		return singleCluster(options)
	}
	return nil
}

func substitute(value any, replacements *strings.Replacer) any {
	switch current := value.(type) {
	case string:
		return replacements.Replace(current)
	case map[string]any:
		for key, item := range current {
			current[key] = substitute(item, replacements)
		}
	case []any:
		for i, item := range current {
			current[i] = substitute(item, replacements)
		}
	}
	return value
}

func renderYAML(data []byte, replacements *strings.Replacer) ([]byte, error) {
	var result bytes.Buffer
	decoder := yamlv3.NewDecoder(bytes.NewReader(data))
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			return result.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
		if document == nil {
			continue
		}
		encoded, err := yaml.Marshal(substitute(document, replacements))
		if err != nil {
			return nil, err
		}
		result.Write(encoded)
		result.WriteString("---\n")
	}
}

func copyExample(options InitOptions, repository, pin string, replacements *strings.Replacer) error {
	return fs.WalkDir(example.Files, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == "." {
				return nil
			}
			return os.MkdirAll(filepath.Join(options.Output, path), 0700)
		}
		data, err := example.Files.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(path, "releases/") {
			var source map[string]any
			if err = yaml.Unmarshal(data, &source); err != nil {
				return err
			}
			spec, ok := source["spec"].(map[string]any)
			if !ok {
				return errors.New("bundle source has no spec")
			}
			spec["url"] = repository
			if filepath.Base(path) != "workers-staging.yaml" {
				spec["ref"] = map[string]string{"digest": "sha256:" + pin}
			}
			data, err = yaml.Marshal(source)
			if err != nil {
				return err
			}
		}
		if strings.HasSuffix(path, ".yaml") {
			data, err = renderYAML(data, replacements)
			if err != nil {
				return err
			}
		} else {
			data = []byte(replacements.Replace(string(data)))
		}
		return os.WriteFile(filepath.Join(options.Output, path), data, 0600)
	})
}

func singleCluster(options InitOptions) error {
	root, err := os.OpenRoot(options.Output)
	if err != nil {
		return err
	}
	defer root.Close()
	all := filepath.Join("bases", "all")
	if err = root.Mkdir(all, 0700); err != nil {
		return err
	}
	for _, name := range []string{"kustomization.yaml", "loco.yaml"} {
		data, readErr := root.ReadFile(filepath.Join("bases", "control-plane", name))
		if readErr != nil {
			return readErr
		}
		if name == "kustomization.yaml" {
			data = []byte(
				"apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\n" +
					"resources: [loco.yaml, ../control-plane/values, ../worker/values]\n",
			)
		} else {
			data = bytes.ReplaceAll(data, []byte("./control-plane"), []byte("./all"))
		}
		if err = root.WriteFile(filepath.Join(all, name), data, 0600); err != nil {
			return err
		}
	}
	control := filepath.Join("clusters", "control-plane")
	worker := filepath.Join("clusters", "worker-staging")
	values, err := root.ReadFile(filepath.Join(worker, "values", "loco-worker.yaml"))
	if err != nil {
		return err
	}
	var workerValues map[string]any
	if err = yaml.Unmarshal(values, &workerValues); err != nil {
		return err
	}
	workerValues["certManager"] = map[string]any{"issuer": map[string]any{"create": false, "email": options.Admin}}
	values, err = yaml.Marshal(workerValues)
	if err != nil {
		return err
	}
	if err = root.WriteFile(filepath.Join(control, "values", "loco-worker.yaml"), values, 0600); err != nil {
		return err
	}
	path := filepath.Join(control, "kustomization.yaml")
	data, err := root.ReadFile(path)
	if err != nil {
		return err
	}
	var cluster map[string]any
	if err = yaml.Unmarshal(data, &cluster); err != nil {
		return err
	}
	cluster["resources"] = []string{"../../releases/control-plane.yaml", "../../bases/all"}
	generators, ok := cluster["configMapGenerator"].([]any)
	if !ok {
		return errors.New("cluster has no values generators")
	}
	cluster["configMapGenerator"] = append(generators, map[string]any{
		"name":      "loco-worker-values",
		"namespace": "flux-system",
		"files":     []string{"values.yaml=values/loco-worker.yaml"},
	})
	data, err = yaml.Marshal(cluster)
	if err != nil {
		return err
	}
	data = bytes.ReplaceAll(
		data,
		[]byte("./clusters/control-plane/config"),
		[]byte("./clusters/"+options.Name+"/config"),
	)
	if err = root.WriteFile(path, data, 0600); err != nil {
		return err
	}
	if err = root.RemoveAll(worker); err != nil {
		return err
	}
	readme := fmt.Sprintf(`# Loco single-cluster repository

This repository installs a control plane and worker on one cluster through Flux.
Bootstrap Flux against clusters/%s with Flux 2.10.0 or later.

The combined base selects the bundle's ./all entry point. releases/control-plane.yaml
pins its bundle digest. Per-release overrides live in clusters/%s/values/;
shared defaults live under bases/. Review endpoints, credentials and certificate settings.

The Secret files are plaintext: encrypt them before committing, or supply the named
Secrets through External Secrets. Keep the seal key in offline custody.
Use locoadm cluster create with --kube-context to deliver the worker join token after Flux bootstrap.

See https://docs.loco.build/reference/installation for enrollment, upgrades and recovery.
`, options.Name, options.Name)
	if err = root.WriteFile("README.md", []byte(readme), 0600); err != nil {
		return err
	}
	if options.Name != "control-plane" {
		return root.Rename(control, filepath.Join("clusters", options.Name))
	}
	return nil
}
