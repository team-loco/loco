package gitops

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	yamlv3 "go.yaml.in/yaml/v3"

	"github.com/team-loco/loco/cmd/internal/dotenv"
	"github.com/team-loco/loco/cmd/internal/host"
	example "github.com/team-loco/loco/examples/gitops"
	"sigs.k8s.io/yaml"
)

type InitOptions struct {
	Output, API, Web, Domain, Region, Admin, Bundle, APIEnvFile string
	SingleCluster                                               bool
	// Postgres runs the API database on a CloudNativePG cluster the bundle installs.
	Postgres bool
	// Registry, when set, is the hostname of a registry the control-plane chart runs.
	Registry string
	Name     string
}

func Init(options InitOptions) error {
	if options.SingleCluster &&
		(len(options.Name) > 63 || !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`).MatchString(options.Name)) {
		return errors.New("name must be a lowercase cluster name")
	}
	apiURL, err := host.ParseURL("--api", options.API)
	if err != nil {
		return err
	}
	webURL, err := host.ParseURL("--web", options.Web)
	if err != nil {
		return err
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
		"us-ca-1",
		options.Region,
		"admin@example.com",
		options.Admin,
		"registry.example.com",
		cmp.Or(options.Registry, "registry.example.com"),
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
	if options.Postgres {
		if err := bundledPostgres(options.Output); err != nil {
			return err
		}
	}
	if options.Registry != "" {
		if err := bundledRegistry(options.Output, options.Registry); err != nil {
			return err
		}
	}
	if options.SingleCluster {
		return singleCluster(options)
	}
	return nil
}

const fieldName = "name"

// postgresOperator applies the bundle's CloudNativePG operator entry point.
const postgresOperator = `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: loco-postgres
  namespace: flux-system
spec:
  interval: 5m
  sourceRef:
    kind: OCIRepository
    name: platform-bundle
  path: ./postgres-operator
  prune: true
  wait: true
  timeout: 30m
`

// bundledPostgres adds the CloudNativePG operator to the control-plane base, makes the cluster
// configuration, and so the control-plane release, wait for it, and turns on postgres.enabled.
func bundledPostgres(output string) error {
	root, err := os.OpenRoot(output)
	if err != nil {
		return err
	}
	defer root.Close()
	base := filepath.Join("bases", "control-plane")
	path := filepath.Join(base, "loco.yaml")
	data, err := root.ReadFile(path)
	if err != nil {
		return err
	}
	var result bytes.Buffer
	decoder := yamlv3.NewDecoder(bytes.NewReader(data))
	for {
		var document map[string]any
		if err = decoder.Decode(&document); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		if metadata, ok := document["metadata"].(map[string]any); ok && metadata[fieldName] == "loco-cluster-config" {
			spec, isMap := document["spec"].(map[string]any)
			dependencies, isList := spec["dependsOn"].([]any)
			if !isMap || !isList {
				return errors.New("loco-cluster-config has no dependsOn list")
			}
			spec["dependsOn"] = append(dependencies, map[string]any{fieldName: "loco-postgres-operator"})
		}
		if err = writeDocument(&result, document); err != nil {
			return err
		}
	}
	result.WriteString(postgresOperator)
	if err = root.WriteFile(path, result.Bytes(), 0600); err != nil {
		return err
	}
	path = filepath.Join(base, "values", "values.yaml")
	if data, err = root.ReadFile(path); err != nil {
		return err
	}
	var values map[string]any
	if err = yaml.Unmarshal(data, &values); err != nil {
		return err
	}
	values["postgres"] = map[string]any{"enabled": true}
	if data, err = yaml.Marshal(values); err != nil {
		return err
	}
	return root.WriteFile(path, data, 0600)
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
		if err = writeDocument(&result, substitute(document, replacements)); err != nil {
			return nil, err
		}
	}
}

// operatorSolverValues are the example values files whose ACME solvers the operator chooses.
var operatorSolverValues = map[string]bool{
	"clusters/control-plane/values/loco-control-plane.yaml": true,
	"clusters/worker-staging/values/loco-worker.yaml":       true,
}

// clearSolvers empties certManager.issuer.solvers in a values file.
func clearSolvers(data []byte) ([]byte, error) {
	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	certManager, ok := values["certManager"].(map[string]any)
	issuer, isMap := certManager["issuer"].(map[string]any)
	if !ok || !isMap {
		return nil, errors.New("example values have no certManager.issuer")
	}
	issuer["solvers"] = []any{}
	return yaml.Marshal(values)
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
		if operatorSolverValues[path] {
			data, err = clearSolvers(data)
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
		fieldName:   "loco-worker-values",
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

func writeDocument(output *bytes.Buffer, document any) error {
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return err
	}
	output.Write(encoded)
	output.WriteString("---\n")
	return nil
}

// bundledRegistry turns on the control-plane chart's registry at hostname.
func bundledRegistry(output, hostname string) error {
	root, err := os.OpenRoot(output)
	if err != nil {
		return err
	}
	defer root.Close()
	path := filepath.Join("clusters", "control-plane", "values", "loco-control-plane.yaml")
	data, err := root.ReadFile(path)
	if err != nil {
		return err
	}
	var values map[string]any
	if err = yaml.Unmarshal(data, &values); err != nil {
		return err
	}
	values["registry"] = map[string]any{"enabled": true, "hostname": hostname}
	if data, err = yaml.Marshal(values); err != nil {
		return err
	}
	return root.WriteFile(path, data, 0600)
}
