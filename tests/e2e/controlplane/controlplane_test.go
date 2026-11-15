package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	release           = "regional"
	upgradeTimeout    = 10 * time.Minute
	requestTimeout    = 15 * time.Second
	images            = "api-failover api ui obs-proxy"
	contentTypeHeader = "Content-Type"
	jsonContent       = "application/json"
	passwordKey       = "password"
	// transitStartupError is the error the API exits with when OpenBao has not created the transit
	// keys yet, the one restart the install tolerates.
	transitStartupError = `loco api failed error="verify the transit keys: `
)

// chartValues are the loco-control-plane values the suite reads: the chart's defaults overlaid
// with tests/e2e/control-plane-values.yaml.
type chartValues struct {
	GatewayNamespace     string `json:"gatewayNamespace"`
	ControlPlaneHostname string `json:"controlPlaneHostname"`
	Gateway              struct {
		Name    string `json:"name"`
		Service struct {
			Patch struct {
				Value struct {
					Spec struct {
						Ports []struct {
							NodePort int `json:"nodePort"`
						} `json:"ports"`
					} `json:"spec"`
				} `json:"value"`
			} `json:"patch"`
		} `json:"service"`
	} `json:"gateway"`
	UI struct {
		Hostname string `json:"hostname"`
	} `json:"ui"`
	ObsProxy struct {
		Ingest struct {
			Hostname string `json:"hostname"`
		} `json:"ingest"`
	} `json:"obsProxy"`
	OpenBao struct {
		Seal struct {
			Static struct {
				ExistingSecret secretRef `json:"existingSecret"`
			} `json:"static"`
		} `json:"seal"`
		Snapshots struct {
			RcloneImage string `json:"rcloneImage"`
			S3          struct {
				Bucket         string `json:"bucket"`
				Prefix         string `json:"prefix"`
				ExistingSecret struct {
					Name               string `json:"name"`
					AccessKeyIDKey     string `json:"accessKeyIdKey"`
					SecretAccessKeyKey string `json:"secretAccessKeyKey"`
				} `json:"existingSecret"`
			} `json:"s3"`
		} `json:"snapshots"`
	} `json:"openbao"`
	ClickHouse struct {
		FullnameOverride string `json:"fullnameOverride"`
		Operator         struct {
			FullnameOverride string `json:"fullnameOverride"`
			Secret           struct {
				Username string `json:"username"`
			} `json:"secret"`
		} `json:"operator"`
		ClickHouse struct {
			Users []struct {
				Name               string   `json:"name"`
				PasswordSecretName string   `json:"password_secret_name"`
				Grants             []string `json:"grants"`
			} `json:"users"`
		} `json:"clickhouse"`
	} `json:"clickhouse"`
}

type chartDependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type secretRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// controlPlane is the installed chart and the port-forwarded Gateway its checks call.
type controlPlane struct {
	values    chartValues
	namespace string
	password  string
	// service is the Envoy proxy Service of the chart's Gateway, and gateway its forwarded URL.
	service string
	gateway string
	// secrets writes secrets as the seeded user; recoveryKeys are OpenBao's before the restore.
	secrets      secretWriter
	recoveryKeys string
}

// TestControlPlane installs the loco-control-plane chart with two API replicas on an empty
// database, then checks in order: routing through the chart's Gateway, OpenBao across an upgrade,
// a snapshot restored into a reinstalled OpenBao, ingest tokens and OTLP ingest, and the API
// retry policy, which reprograms the Gateway.
func TestControlPlane(t *testing.T) {
	t.Parallel()
	cp := install(t)
	for _, step := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"the Gateway routes the API and dashboard", cp.checkRouting},
		{"OpenBao keeps its keys across an upgrade", cp.checkUpgrade},
		{"a snapshot restores into a reinstalled OpenBao", cp.checkRestore},
		{"the API issues ingest tokens the ingest proxy takes", cp.checkIngest},
		{"the API retries only requests that never reached a replica", cp.checkFailover},
	} {
		if !t.Run(step.name, step.run) {
			return
		}
	}
}

func install(t *testing.T) *controlPlane {
	t.Helper()
	cp := &controlPlane{}
	valuesFile := c.Path("tests", "e2e", "control-plane-values.yaml")
	mustReadYAML(t, c.Path("charts", "loco-control-plane", "values.yaml"), &cp.values)
	mustReadYAML(t, valuesFile, &cp.values)
	cp.namespace = cp.values.GatewayNamespace
	t.Cleanup(func() {
		if t.Failed() {
			cp.logDiagnostics(t)
		}
	})
	loadImages(t)

	var worker struct {
		Dependencies []chartDependency `json:"dependencies"`
	}
	mustReadYAML(t, c.Path("charts", "loco-worker", "Chart.yaml"), &worker)
	gateway := slices.IndexFunc(worker.Dependencies, func(d chartDependency) bool { return d.Name == "gateway-helm" })
	var workerValues map[string]any
	mustReadYAML(t, c.Path("charts", "loco-worker", "values.yaml"), &workerValues)
	helm(t, "upgrade", "--install", "eg",
		c.Path("charts", "loco-worker", "charts", "gateway-helm-"+worker.Dependencies[gateway].Version+".tgz"),
		"--namespace", cp.namespace, "--create-namespace",
		"--values", writeJSON(t, "gateway-helm-values.json", workerValues["gateway-helm"]),
		"--wait", "--timeout", installTimeout.String())

	cp.password = persisted(t, "password", func() string { return randomHex(t, 24) })
	applySecret(t, cp.namespace, "fixture-password", map[string]string{passwordKey: cp.password})
	snapshots := cp.values.OpenBao.Snapshots
	applySecret(t, cp.namespace, snapshots.S3.ExistingSecret.Name, map[string]string{
		snapshots.S3.ExistingSecret.AccessKeyIDKey: persisted(
			t,
			"snapshot-key-id",
			func() string { return randomHex(t, 8) },
		),
		snapshots.S3.ExistingSecret.SecretAccessKeyKey: persisted(
			t,
			"snapshot-key",
			func() string { return randomHex(t, 24) },
		),
	})
	var compose struct {
		Services struct {
			Postgres struct {
				Image string `json:"image"`
			} `json:"postgres"`
		} `json:"services"`
	}
	mustReadYAML(t, c.Path("dev", "compose.yaml"), &compose)
	helm(t, "upgrade", "--install", "fixtures", c.Path("tests", "e2e", "fixtures", "control-plane"),
		"--namespace", cp.namespace, "--values", writeJSON(t, "fixtures.json", map[string]string{
			"postgresImage":              compose.Services.Postgres.Image,
			"rcloneImage":                snapshots.RcloneImage,
			"snapshotBucket":             snapshots.S3.Bucket,
			"snapshotSecret":             snapshots.S3.ExistingSecret.Name,
			"snapshotAccessKeyIdKey":     snapshots.S3.ExistingSecret.AccessKeyIDKey,
			"snapshotSecretAccessKeyKey": snapshots.S3.ExistingSecret.SecretAccessKeyKey,
		}), "--wait", "--timeout", installTimeout.String())

	seal := cp.values.OpenBao.Seal.Static.ExistingSecret
	applySecret(t, cp.namespace, seal.Name, map[string]string{
		seal.Key: persisted(t, "seal-key", func() string { return randomBase64(t, 32) }),
	})
	applySecret(t, cp.namespace, "api-env", map[string]string{
		"DATABASE_URL":                   "postgresql://loco:" + cp.password + "@postgres:5432/loco?sslmode=disable",
		"LOCO_IMAGE_RETENTION":           "0",
		"LOCO_SECRETS_TRANSIT_CACHE_TTL": "1s",
	})
	// The component fixtures own their database identities, which locoadm creates in an install.
	clickhouse := cp.values.ClickHouse
	applySecret(t, cp.namespace, clickhouse.Operator.FullnameOverride, map[string]string{
		"username": clickhouse.Operator.Secret.Username, passwordKey: cp.password,
	})
	for _, user := range clickhouse.ClickHouse.Users {
		applySecret(t, cp.namespace, user.PasswordSecretName, map[string]string{
			passwordKey: cp.password,
			"url": fmt.Sprintf("clickhouse://%s:%s@clickhouse-%s.%s.svc.cluster.local:9000",
				user.Name, cp.password, clickhouse.FullnameOverride, cp.namespace),
		})
	}
	cp.upgrade(t)
	cp.checkAPIRestarts(t)

	selector := "gateway.envoyproxy.io/owning-gateway-name=" + cp.values.Gateway.Name
	platform.Eventually(t, time.Minute, "the Envoy proxy Deployment", func(ctx context.Context) error {
		found, err := c.Exists(ctx, "-n", cp.namespace, "deployments", "-l", selector)
		if err == nil && !found {
			return errors.New("not created yet")
		}
		return err
	})
	c.MustKubectl(t, "-n", cp.namespace, "rollout", "status", "deployment", "-l", selector,
		"--timeout", installTimeout.String())
	cp.service = strings.TrimSpace(c.MustKubectl(t, "-n", cp.namespace, "get", "services", "-l", selector,
		"-o", "jsonpath={.items[0].metadata.name}"))
	cp.gateway = "http://127.0.0.1:" + strconv.Itoa(c.PortForward(t, cp.namespace, "service/"+cp.service, 80))
	return cp
}

// loadImages pulls the images E2E_IMAGES names, or builds them, and loads them into the cluster.
func loadImages(t *testing.T) {
	t.Helper()
	names := strings.Fields(images)
	if prebuilt := os.Getenv("E2E_IMAGES"); prebuilt != "" {
		for _, name := range names {
			if err := c.PullPrebuilt(
				t.Context(),
				strings.ReplaceAll(prebuilt, "*", name),
				"loco-"+name+":e2e",
			); err != nil {
				t.Fatal(err)
			}
		}
	} else if !c.SkipBuild() {
		// docker-bake.hcl tags each image REGISTRY-<target>:TAG; the Docker image store holds no provenance.
		bake := append([]string{"REGISTRY=loco", "TAG=e2e", "VERSION=" + c.Version, "docker", "buildx", "bake",
			"-f", c.Path("docker-bake.hcl"), "--load", "--provenance=false"}, names...)
		if _, err := c.Command(t.Context(), "env", bake...); err != nil {
			t.Fatal(err)
		}
	}
	load := make([]string, 0, len(names)+2)
	load = append(load, "load", "docker-image")
	for _, name := range names {
		load = append(load, "loco-"+name+":e2e")
	}
	if _, err := c.Kind(t.Context(), load...); err != nil {
		t.Fatal(err)
	}
}

func (cp *controlPlane) upgrade(t *testing.T) {
	t.Helper()
	helm(t, "upgrade", "--install", release, c.Path("charts", "loco-control-plane"), "--namespace", cp.namespace,
		"-f", c.Path("tests", "e2e", "control-plane-values.yaml"), "--wait", "--wait-for-jobs",
		"--timeout", upgradeTimeout.String())
}

// checkAPIRestarts fails unless every restart of an API pod was the startup race with OpenBao.
func (cp *controlPlane) checkAPIRestarts(t *testing.T) {
	t.Helper()
	var pods struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				ContainerStatuses []struct {
					RestartCount int             `json:"restartCount"`
					LastState    json.RawMessage `json:"lastState"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	c.MustGet(t, &pods, "-n", cp.namespace, "pods", "-l", "app="+release+"-api")
	for _, pod := range pods.Items {
		status := pod.Status.ContainerStatuses[0]
		if status.RestartCount == 0 {
			continue
		}
		previous := c.MustKubectl(t, "-n", cp.namespace, "logs", pod.Metadata.Name, "--previous")
		if !strings.Contains(previous, transitStartupError) {
			t.Fatalf("API pod %s restarted %d times without logging why it could not verify the transit keys; "+
				"last state: %s", pod.Metadata.Name, status.RestartCount, status.LastState)
		}
	}
}

//nolint:thelper // a step of TestControlPlane
func (cp *controlPlane) checkRouting(t *testing.T) {
	listeners := c.MustKubectl(t, "-n", cp.namespace, "get", "gateway/"+cp.values.Gateway.Name,
		"-o", "jsonpath={.spec.listeners[*].hostname}")
	want := strings.Join([]string{cp.values.UI.Hostname, cp.values.ControlPlaneHostname,
		cp.values.ObsProxy.Ingest.Hostname}, " ")
	if listeners != want {
		t.Errorf("the chart Gateway listens for %q, want exactly %q", listeners, want)
	}
	nodePort := c.MustKubectl(t, "-n", cp.namespace, "get", "service/"+cp.service,
		"-o", "jsonpath={.spec.ports[?(@.port==80)].nodePort}")
	if want := strconv.Itoa(cp.values.Gateway.Service.Patch.Value.Spec.Ports[0].NodePort); nodePort != want {
		t.Errorf("the Gateway Service's HTTP NodePort is %s, want the configured %s", nodePort, want)
	}

	api := cp.values.ControlPlaneHostname
	cp.request(t, http.MethodGet, api, "/health", "", nil).expect(t, http.StatusOK)
	var openapi struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]map[string]any `json:"paths"`
	}
	cp.request(t, http.MethodGet, api, "/openapi.json", "", nil).decode(t, &openapi)
	if openapi.OpenAPI != "3.1.0" || openapi.Info.Version != c.Version {
		t.Errorf("openapi.json is OpenAPI %s for version %s, want 3.1.0 for %s",
			openapi.OpenAPI, openapi.Info.Version, c.Version)
	}
	for _, path := range []string{"/loco.config.v1.ConfigService/GetConfig", "/loco.agent.v1.AgentService/Sync",
		"/loco.observability.v1.ObservabilityService/QueryLogs"} {
		if openapi.Paths[path]["post"] == nil {
			t.Errorf("openapi.json has no POST %s", path)
		}
	}
	docs := cp.request(t, http.MethodGet, api, "/", "", nil).text(t)
	for _, want := range []string{"Scalar.createApiReference(", `url: "/openapi.json"`} {
		if !strings.Contains(docs, want) {
			t.Errorf("the API docs page lacks %s", want)
		}
	}
	cp.request(t, http.MethodGet, api, "/docs", "", nil).expect(t, http.StatusNotFound)

	app := cp.values.UI.Hostname
	dashboard := cp.request(t, http.MethodGet, app, "/", "", nil).text(t)
	runtimeScript := strings.Index(dashboard, "/runtime-config.js")
	if !strings.Contains(dashboard, `<script src="/runtime-config.js"></script>`) ||
		runtimeScript > strings.Index(dashboard, `type="module"`) {
		t.Errorf("the dashboard does not load /runtime-config.js before its module script:\n%s", dashboard)
	}
	runtime := cp.request(t, http.MethodGet, app, "/runtime-config.js", "", nil)
	for name, values := range runtime.header {
		if strings.Contains(strings.ToLower(name+": "+strings.Join(values, ",")), "immutable") {
			t.Errorf("/runtime-config.js is served with %s: %s", name, values)
		}
	}
	if config := strings.TrimSpace(runtime.text(t)); config != `window.locoConfig = {"apiURL":"/api"};` {
		t.Errorf("/runtime-config.js = %s", config)
	}
	var config struct {
		ServiceDefaults json.RawMessage `json:"serviceDefaults"`
		OrgLinks        []struct {
			Label string `json:"label"`
			URL   string `json:"url"`
		} `json:"orgLinks"`
	}
	cp.request(t, http.MethodPost, app, "/api/loco.config.v1.ConfigService/GetConfig", "{}", nil).decode(t, &config)
	if config.ServiceDefaults == nil {
		t.Error("GetConfig through the dashboard host returns no serviceDefaults")
	}
	if len(config.OrgLinks) != 1 || config.OrgLinks[0].Label != "Support" ||
		config.OrgLinks[0].URL != "https://support.e2e.test.local/orgs/{orgId}" {
		t.Errorf("GetConfig returns the organization links %+v", config.OrgLinks)
	}
	now := time.Now().UTC()
	query, err := json.Marshal(map[string]string{
		"workspaceId": "00000000-0000-0000-0000-000000000001",
		"startTime":   now.Add(-5 * time.Minute).Format(time.RFC3339),
		"endTime":     now.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	var refused struct {
		Code string `json:"code"`
	}
	cp.request(t, http.MethodPost, app, "/api/loco.observability.v1.ObservabilityService/QueryLogs", string(query),
		bearer("invalid-e2e-token")).decodeAny(t, &refused)
	if refused.Code != "unauthenticated" {
		t.Errorf("QueryLogs with an invalid token answered %q, want unauthenticated", refused.Code)
	}
}

// reply is a response read whole.
type reply struct {
	what   string
	status int
	header http.Header
	body   []byte
}

// request sends a request for host through the Gateway; a JSON body is sent as JSON unless header
// names another type.
func (cp *controlPlane) request(t *testing.T, method, host, path, body string, header http.Header) reply {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, cp.gateway+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	// A kept-alive connection stays on the Envoy listener it opened on, which a changed Gateway drains.
	request.Close = true
	maps.Copy(request.Header, header)
	if body != "" && request.Header.Get(contentTypeHeader) == "" {
		request.Header.Set(contentTypeHeader, jsonContent)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{what: method + " " + path + " for " + host, status: response.StatusCode, header: response.Header,
		body: data}
}

// bearer is the header of a request with token.
func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func (r reply) expect(t *testing.T, status int) []byte {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: %d, want %d: %s", r.what, r.status, status, r.body)
	}
	return r.body
}

func (r reply) text(t *testing.T) string {
	t.Helper()
	return string(r.expect(t, http.StatusOK))
}

func (r reply) decode(t *testing.T, into any) {
	t.Helper()
	r.expect(t, http.StatusOK)
	r.decodeAny(t, into)
}

// decodeAny decodes a reply of any status.
func (r reply) decodeAny(t *testing.T, into any) {
	t.Helper()
	if err := json.Unmarshal(r.body, into); err != nil {
		t.Fatalf("decode %s: %v: %s", r.what, err, r.body)
	}
}

func (cp *controlPlane) logDiagnostics(t *testing.T) {
	t.Helper()
	ns := "-n " + cp.namespace + " "
	c.LogKubectl(t, ns+"get pods,events -o wide",
		ns+"logs -l app="+release+"-api --all-pods=true --tail=200",
		ns+"describe pods -l app="+release+"-api",
		ns+"logs -l app="+release+"-api --all-pods=true --previous --prefix --tail=200",
		ns+"logs deployment/"+release+"-obs-proxy-ingest --tail=200",
		ns+"get gateways,httproutes,clickhouseinstallations -o yaml",
		ns+"logs -l app="+release+"-openbao --all-containers --tail=200",
		ns+"logs -l app="+release+"-openbao-setup --all-containers --tail=200")
}

// persisted returns the value stored under name in the run's directory, generating and storing it
// on the first run, so a kept install keeps its credentials across runs.
func persisted(t *testing.T, name string, generate func() string) string {
	t.Helper()
	path := filepath.Join(c.Dir(), name)
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return string(data)
	}
	value := generate()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return value
}
