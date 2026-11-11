package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"sigs.k8s.io/yaml"
)

var (
	errDockerBuildx              = errors.New("installation fixture requires the Docker Buildx plugin")
	errDependencyPin             = errors.New("fixture dependency pin is missing")
	errDockerPlatform            = errors.New("installation fixture requires a Linux Docker daemon")
	errProviderPasswordPin       = errors.New("fixture IdP has no pinned password hash")
	errRegistryAccessConfig      = errors.New("invalid fixture registry access configuration")
	errRegistryAddress           = errors.New("registry fixture has no service address")
	errRegistryHTTPConfig        = errors.New("invalid fixture registry HTTP configuration")
	errRegistryReadinessEndpoint = errors.New("invalid registry readiness endpoint")
	errRegistryRepositoryConfig  = errors.New("invalid fixture registry repository configuration")
)

const (
	registryForwardTimeout = time.Minute
	registryRequestTimeout = time.Second
	registryProbeInterval  = 100 * time.Millisecond
	fixtureRegistry        = "registry"
	fixtureBuilder         = "builder"
	fixtureDex             = "dex"
	sourceService          = "s3-source"
	fixtureNamespace       = "installation-fixtures"
	controlNamespace       = "loco-control-plane"
	workerNamespace        = "loco-staging"
	registryHost           = "registry.installation.localhost:5000"
	fixtureReadyTimeout    = "300s"
	registryReadAction     = "read"
)

type fixture struct {
	root, dir, name, kubeconfig, registryURL string
	skip, ciCache                            bool
	forward                                  *exec.Cmd
	cloudManaged                             bool
	platform                                 string
	dockerPluginDirs                         []string
}

func newFixture(root string, skip, ciCache bool) (*fixture, error) {
	digest := sha256.Sum256([]byte(root))
	name := "loco-installation-" + hex.EncodeToString(digest[:4])
	directory := filepath.Join(root, "tests", "e2e", "runs", name)
	if checkErr := os.MkdirAll(directory, 0700); checkErr != nil {
		return nil, checkErr
	}
	return &fixture{
		root:       root,
		dir:        directory,
		name:       name,
		kubeconfig: filepath.Join(directory, "kubeconfig"),
		skip:       skip,
		ciCache:    ciCache,
	}, nil
}

func (f *fixture) acceptance(ctx context.Context) error {
	for _, program := range []string{"docker", "kind", kubectlExecutable, helmExecutable, "mise", "yq"} {
		if _, checkErr := exec.LookPath(program); checkErr != nil {
			return fmt.Errorf("required fixture tool %s: %w", program, checkErr)
		}
	}
	platform, platformErr := command(
		ctx,
		f.root,
		nil,
		"docker",
		"version",
		"--format",
		"{{.Server.Os}}/{{.Server.Arch}}",
	)
	if platformErr != nil {
		return platformErr
	}
	f.platform = strings.TrimSpace(string(platform))
	if !strings.HasPrefix(f.platform, "linux/") {
		return errDockerPlatform
	}
	metadata, metadataErr := command(ctx, f.root, nil, "docker", "info", "--format", "{{json .ClientInfo.Plugins}}")
	if metadataErr != nil {
		return metadataErr
	}
	var plugins []struct {
		Name string `json:"Name"`
		Path string `json:"Path"`
	}
	if json.Unmarshal(metadata, &plugins) != nil {
		return errDockerBuildx
	}
	var buildx bool
	for _, plugin := range plugins {
		f.dockerPluginDirs = append(f.dockerPluginDirs, filepath.Dir(plugin.Path))
		buildx = buildx || plugin.Name == "buildx"
	}
	if !buildx {
		return errDockerBuildx
	}
	if checkErr := os.Setenv("KUBECONFIG", f.kubeconfig); checkErr != nil {
		return checkErr
	}
	clusters, err := command(ctx, f.root, nil, "kind", "get", "clusters")
	if err != nil {
		return err
	}
	if !strings.Contains("\n"+string(clusters), "\n"+f.name+"\n") {
		if _, checkErr := command(
			ctx,
			f.root,
			nil,
			"kind",
			"create",
			"cluster",
			"--name",
			f.name,
			"--config",
			"tests/e2e/fixtures/installation/kind.yaml",
			"--kubeconfig",
			f.kubeconfig,
			"--wait",
			fixtureReadyTimeout,
		); checkErr != nil {
			return checkErr
		}
	}
	if _, checkErr := command(
		ctx,
		f.root,
		nil,
		"kind",
		"export",
		"kubeconfig",
		"--name",
		f.name,
		"--kubeconfig",
		f.kubeconfig,
	); checkErr != nil {
		return checkErr
	}
	// kubeadm excludes the control-plane node from load balancers, but the fixture schedules
	// workloads there too; a Local-policy Gateway proxy placed on it would be unreachable.
	if _, checkErr := command(
		ctx,
		f.root,
		nil,
		"kubectl",
		"label",
		"nodes",
		"-l",
		"node-role.kubernetes.io/control-plane",
		"node.kubernetes.io/exclude-from-external-load-balancers-",
	); checkErr != nil {
		return checkErr
	}
	if cloudErr := f.startLoadBalancer(ctx); cloudErr != nil {
		return cloudErr
	}
	fmt.Println("Provisioning external installation fixtures")
	if checkErr := f.provision(ctx); checkErr != nil {
		return checkErr
	}
	if checkErr := f.forwardRegistry(ctx); checkErr != nil {
		return checkErr
	}
	fmt.Println("Publishing fixture release artifacts")
	manifest, operatorImage, err := f.release(ctx)
	if err != nil {
		return err
	}
	if checkErr := f.configureClient(ctx, manifest, operatorImage); checkErr != nil {
		return checkErr
	}
	if _, checkErr := command(
		ctx,
		f.root,
		nil,
		"kubectl",
		"-n",
		fixtureNamespace,
		"wait",
		"--for=condition=Ready",
		"pod/operator",
		"--timeout="+fixtureReadyTimeout,
	); checkErr != nil {
		return checkErr
	}
	fmt.Println("Running public operator and application workflow")
	return f.runOperator(ctx)
}

func (f *fixture) runOperator(ctx context.Context) error {
	out, err := operatorWorkflow(ctx, f.root)
	if err != nil {
		inspection, cancel := context.WithTimeout(context.WithoutCancel(ctx), failureSnapshotTimeout)
		defer cancel()
		f.failureSnapshot(inspection)
		return fmt.Errorf("installation acceptance failed: %w", err)
	}
	fmt.Print(string(out))
	return nil
}

func (f *fixture) cleanup(ctx context.Context, keep bool) error {
	var stopErr, cloudErr, removeErr error
	if f.forward != nil {
		stopErr = stopProcess(f.forward)
	}
	if keep {
		fmt.Println("Kept installation fixture:", f.name)
		return stopErr
	}
	_, kindErr := command(ctx, f.root, nil, "kind", "delete", "cluster", "--name", f.name)
	if f.cloudManaged {
		_, cloudErr = command(
			ctx,
			f.root,
			nil,
			"docker",
			"compose",
			"-p",
			f.name,
			"-f",
			"dev/compose.cloud-provider-kind.yaml",
			"down",
		)
	}
	if kindErr == nil {
		removeErr = os.RemoveAll(f.dir)
	}
	return errors.Join(stopErr, kindErr, cloudErr, removeErr)
}

func (f *fixture) apply(ctx context.Context, objects ...map[string]any) error {
	var documents bytes.Buffer
	for _, object := range objects {
		data, err := yaml.Marshal(object)
		if err != nil {
			return err
		}
		documents.Write(data)
		documents.WriteString("\n---\n")
	}
	_, err := command(ctx, f.root, documents.Bytes(), "kubectl", "apply", "-f", "-")
	return err
}

func (f *fixture) pin(ctx context.Context, file, expression string) (string, error) {
	output, err := command(ctx, f.root, nil, "yq", "-r", expression, file)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" || value == "null" {
		return "", errDependencyPin
	}
	return value, nil
}

type fixtureService struct {
	Name    string
	Image   string
	Port    int
	Command []string
	Args    []string
}

func (f *fixture) provision(ctx context.Context) error {
	pins := map[string]string{}
	for _, service := range []string{"postgres", fixtureDex, fixtureRegistry} {
		image, err := f.pin(ctx, "dev/compose.yaml", ".services."+service+".image")
		if err != nil {
			return err
		}
		pins[service] = image
	}
	s3, err := f.pin(ctx, "charts/loco-control-plane/values.yaml", ".openbao.snapshots.rcloneImage")
	if err != nil {
		return err
	}
	services := []fixtureService{
		{Name: "postgres", Image: pins["postgres"], Port: 5432},
		{
			Name:    fixtureDex,
			Image:   pins[fixtureDex],
			Port:    5556,
			Command: []string{fixtureDex, "serve", "/config/dex.yaml"},
		},
		{
			Name:  fixtureRegistry,
			Image: pins[fixtureRegistry],
			Port:  5000,
			Args:  []string{"serve", "/config/registry.json"},
		},
		{
			Name:  "s3",
			Image: s3,
			Port:  8080,
			Command: []string{
				"sh",
				"-c",
				"mkdir -p /data/backups && exec rclone serve s3 --addr :8080 --auth-key fixture-access,fixture-source-canary /data",
			},
		},
		{
			Name:  sourceService,
			Image: s3,
			Port:  8080,
			Command: []string{
				"sh",
				"-c",
				"mkdir -p /data/source && exec rclone serve s3 --force-path-style=false " +
					"--addr :8080 --auth-key fixture-access,fixture-source-canary /data",
			},
		},
	}
	registryData, err := f.registryConfiguration()
	if err != nil {
		return err
	}
	htpasswd, err := os.ReadFile(filepath.Join(f.root, "dev", fixtureRegistry, "htpasswd"))
	if err != nil {
		return err
	}
	dex, err := os.ReadFile(filepath.Join(f.root, "dev", "dex.yaml"))
	if err != nil {
		return err
	}
	hash := ""
	for line := range strings.SplitSeq(string(dex), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "hash:"); ok {
			hash, err = strconv.Unquote(strings.TrimSpace(value))
			if err != nil {
				return err
			}
			break
		}
	}
	if hash == "" {
		return errProviderPasswordPin
	}

	registryRevision := sha256.Sum256(registryData)
	parameters := map[string]any{
		"RegistryConfig": string(registryData),
		"ConfigRevision": hex.EncodeToString(registryRevision[:]),
		"Htpasswd":       string(htpasswd),
		"Email":          fixtureEmail,
		"PasswordHash":   hash,
		"Services":       services,
	}
	if checkErr := f.renderFixture(ctx, "services.yaml", parameters); checkErr != nil {
		return checkErr
	}
	for _, service := range services {
		if _, checkErr := command(
			ctx,
			f.root,
			nil,
			"kubectl",
			"-n",
			fixtureNamespace,
			"rollout",
			"status",
			"deployment/"+service.Name,
			"--timeout="+fixtureReadyTimeout,
		); checkErr != nil {
			return checkErr
		}
	}
	return f.registryRouting(ctx)
}

func metadata(name, namespace string) map[string]any {
	return map[string]any{"name": name, "namespace": namespace}
}

func (f *fixture) renderFixture(ctx context.Context, file string, parameters map[string]any) error {
	path := filepath.Join(f.root, "tests", "e2e", "fixtures", "installation", file)
	data, err := renderFixtureFile(path, parameters)
	if err != nil {
		return err
	}
	_, err = command(ctx, f.root, data, "kubectl", "apply", "-f", "-")
	return err
}

func renderFixtureFile(path string, parameters map[string]any) ([]byte, error) {
	functions := template.FuncMap{
		"indent": func(value string, width int) string {
			return strings.TrimRight(
				strings.Repeat(" ", width)+strings.ReplaceAll(value, "\n", "\n"+strings.Repeat(" ", width)),
				" ",
			)
		},
		"quote": strconv.Quote,
		"json":  func(value any) (string, error) { data, err := json.Marshal(value); return string(data), err },
	}
	fixture, err := template.New(filepath.Base(path)).Option("missingkey=error").Funcs(functions).ParseFiles(path)
	if err != nil {
		return nil, err
	}
	var data bytes.Buffer
	if checkErr := fixture.Execute(&data, parameters); checkErr != nil {
		return nil, checkErr
	}
	return data.Bytes(), nil
}

func (f *fixture) registryRouting(ctx context.Context) error {
	data, err := command(
		ctx,
		f.root,
		nil,
		"kubectl",
		"-n",
		fixtureNamespace,
		"get",
		"service",
		fixtureRegistry,
		"-o",
		"jsonpath={.spec.clusterIP}",
	)
	if err != nil {
		return err
	}
	address := strings.TrimSpace(string(data))
	if net.ParseIP(address) == nil {
		return errRegistryAddress
	}
	// CoreDNS makes the declared registry host available to API/build pods. Containerd
	// nodes use the same service IP through a registry mirror, without modifying host DNS.
	core, err := command(
		ctx,
		f.root,
		nil,
		"kubectl",
		"-n",
		"kube-system",
		"get",
		"configmap",
		"coredns",
		"-o",
		"jsonpath={.data.Corefile}",
	)
	if err != nil {
		return err
	}
	sourceIP, err := command(
		ctx,
		f.root,
		nil,
		"kubectl",
		"-n",
		fixtureNamespace,
		"get",
		"service",
		sourceService,
		"-o",
		"jsonpath={.spec.clusterIP}",
	)
	if err != nil {
		return err
	}
	text := string(core)
	if !strings.Contains(text, "registry.installation.localhost") {
		text = strings.Replace(
			text,
			"    kubernetes cluster.local",
			"    hosts {\n        "+address+" registry.installation.localhost\n        "+strings.TrimSpace(
				string(sourceIP),
			)+" s3.installation.localhost source.s3.installation.localhost\n"+
				"        fallthrough\n    }\n    kubernetes cluster.local",
			1,
		)
	}
	if checkErr := f.apply(
		ctx,
		map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   metadata("coredns", "kube-system"),
			"data":       map[string]string{"Corefile": text},
		},
	); checkErr != nil {
		return checkErr
	}
	nodes, err := command(ctx, f.root, nil, "kind", "get", "nodes", "--name", f.name)
	if err != nil {
		return err
	}
	for node := range strings.FieldsSeq(string(nodes)) {
		config := fmt.Sprintf(
			"server = \"http://%s\"\n[host.\"http://%s:5000\"]\n  capabilities = [\"pull\", \"resolve\"]\n",
			registryHost,
			address,
		)
		directory := "/etc/containerd/certs.d/" + registryHost
		script := fmt.Sprintf("mkdir -p %[1]s && cat > %[1]s/hosts.toml", directory)
		if _, checkErr := command(
			ctx,
			f.root,
			[]byte(config),
			"docker",
			"exec",
			"-i",
			node,
			"sh",
			"-c",
			script,
		); checkErr != nil {
			return checkErr
		}
	}
	return nil
}

func (f *fixture) forwardRegistry(ctx context.Context) error {
	portPath := filepath.Join(f.dir, "registry-port")
	port, err := os.ReadFile(portPath)
	if os.IsNotExist(err) {
		listener, listenErr := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
		if listenErr != nil {
			return listenErr
		}
		_, value, splitErr := net.SplitHostPort(listener.Addr().String())
		if closeErr := listener.Close(); closeErr != nil {
			return closeErr
		}
		if splitErr != nil {
			return splitErr
		}
		port = []byte(value)
		if writeErr := os.WriteFile(portPath, port, 0600); writeErr != nil {
			return writeErr
		}
	} else if err != nil {
		return err
	}

	cmd := exec.CommandContext(
		ctx,
		"kubectl",
		"-n",
		fixtureNamespace,
		"port-forward",
		"service/registry",
		string(port)+":5000",
		"--address=127.0.0.1",
	)

	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if startErr := cmd.Start(); startErr != nil {
		return startErr
	}
	f.forward = cmd
	f.registryURL = "http://127.0.0.1:" + string(port)
	return waitRegistry(ctx, f.registryURL)
}

func waitRegistry(ctx context.Context, endpoint string) error {
	deadline, cancel := context.WithTimeout(ctx, registryForwardTimeout)
	defer cancel()
	client := &http.Client{Timeout: registryRequestTimeout}
	defer client.CloseIdleConnections()
	retry := time.NewTicker(registryProbeInterval)
	defer retry.Stop()
	for {
		request, err := http.NewRequestWithContext(deadline, http.MethodGet, endpoint+"/v2/", nil)
		if err != nil {
			return errRegistryReadinessEndpoint
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, fixtureResponseLimit))
			closeErr := response.Body.Close()
			ready := response.StatusCode == http.StatusOK || response.StatusCode == http.StatusUnauthorized
			if ready && readErr == nil && closeErr == nil {
				return nil
			}
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("wait for registry fixture: %w", deadline.Err())
		case <-retry.C:
		}
	}
}

func (f *fixture) registryConfiguration() ([]byte, error) {
	registryData, err := os.ReadFile(filepath.Join(f.root, "dev", fixtureRegistry, "config.json"))
	if err != nil {
		return nil, err
	}
	var registry map[string]any
	if checkErr := json.Unmarshal(registryData, &registry); checkErr != nil {
		return nil, checkErr
	}
	httpConfig, ok := registry["http"].(map[string]any)
	if !ok {
		return nil, errRegistryHTTPConfig
	}
	access, ok := httpConfig["accessControl"].(map[string]any)
	if !ok {
		return nil, errRegistryAccessConfig
	}
	repos, ok := access["repositories"].(map[string]any)
	if !ok {
		return nil, errRegistryRepositoryConfig
	}
	repos["platform/**"] = map[string]any{
		"anonymousPolicy": []string{registryReadAction},
		"defaultPolicy":   []string{registryReadAction},
		"policies": []any{
			map[string]any{
				"users":   []string{fixtureBuilder},
				"actions": []string{registryReadAction, "create", "update"},
			},
		},
	}
	httpConfig["auth"] = map[string]any{"htpasswd": map[string]string{"path": "/config/htpasswd"}}
	httpConfig["compat"] = []string{"docker2s2"}
	return json.Marshal(registry)
}
