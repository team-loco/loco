package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"sigs.k8s.io/yaml"
)

func testConfig(t *testing.T) config {
	t.Helper()
	t.Chdir("../..")
	return config{
		Cluster:       "loco-cluster-local-2",
		Domain:        "loco-dev-2.localhost",
		State:         t.TempDir(),
		Project:       "loco-dev-2",
		AppPort:       "8200",
		AppsPort:      "8643",
		WebPort:       "5373",
		DexPort:       "5756",
		RegistryPort:  "5201",
		S3Port:        "9200",
		ClusterUID:    "00000000-0000-0000-0000-000000000001",
		PrivateEgress: `[{"cidr":"172.18.0.0/16","port":5201}]`,
	}
}

func TestInstallationTemplateMatchesSchema(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.render("dev/loco-install.yaml", cfg.file()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfg.file())
	if err != nil {
		t.Fatal(err)
	}
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err = json.Unmarshal(jsonData, &value); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	schema, err := compiler.Compile("schemas/installation.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatal(err)
	}
	var install struct {
		ControlPlane struct {
			APIURL, WebURL string
			Admins         []string
		}
		Workers []struct{ Name, AppsDomain string }
	}
	if err := yaml.Unmarshal(data, &install); err != nil {
		t.Fatal(err)
	}
	if install.ControlPlane.APIURL != cfg.apiURL() || install.ControlPlane.WebURL != "http://localhost:5373" ||
		len(install.ControlPlane.Admins) != 1 || install.ControlPlane.Admins[0] != "dev@loco.test" ||
		len(
			install.Workers,
		) != 1 || install.Workers[0].Name != "local" || install.Workers[0].AppsDomain != "apps."+cfg.Domain {
		t.Fatalf("unexpected installation: %+v", install)
	}
}

func TestKindPortMappings(t *testing.T) {
	cfg := testConfig(t)
	path := filepath.Join(cfg.State, "kind.yaml")
	if err := cfg.render("dev/kind-local.yaml", path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cluster struct {
		Nodes []struct {
			ExtraPortMappings []struct {
				HostPort, ContainerPort int
				ListenAddress           string
			}
		}
	}
	if err := yaml.Unmarshal(data, &cluster); err != nil {
		t.Fatal(err)
	}
	if len(cluster.Nodes) != 1 {
		t.Fatalf("nodes: %+v", cluster.Nodes)
	}
	ports := cluster.Nodes[0].ExtraPortMappings
	if len(ports) != 2 || ports[0].HostPort != 8200 || ports[0].ContainerPort != 30080 ||
		ports[1].HostPort != 8643 || ports[1].ContainerPort != 30443 ||
		ports[0].ListenAddress != "127.0.0.1" || ports[1].ListenAddress != "127.0.0.1" {
		t.Fatalf("ports: %+v", ports)
	}
}

func TestInstallInputsTrackChartsAndConfiguration(t *testing.T) {
	cfg := testConfig(t)
	// Copy only the files the stamp reads into a disposable checkout.
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	for _, name := range []string{
		"dev/loco-install.yaml", "dev/kind-local.yaml", "dev/compose.local.yaml",
		"dev/dex.yaml", "dev/registry/config.json", "charts/worker/values.yaml",
		"manifests/namespaces/loco-system.yaml",
	} {
		data := []byte("values: {}\n")
		if !strings.HasPrefix(name, "charts/") {
			data, err = os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(temp, name)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(temp)
	before, err := cfg.inputsHash()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"charts/worker/values.yaml", "dev/loco-install.yaml"} {
		if err = os.WriteFile(path, []byte("changed: true\n"), 0600); err != nil {
			t.Fatal(err)
		}
		after, hashErr := cfg.inputsHash()
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		if before == after {
			t.Fatalf("stamp ignores %s", path)
		}
		before = after
	}
	cfg.AppPort = "8300"
	after, err := cfg.inputsHash()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("stamp ignores port changes")
	}
}

func TestRunningRequiresAnEnrolledWorkerAndClaimedAdmin(t *testing.T) {
	for _, tc := range []struct {
		data string
		want bool
	}{
		{`{"phase":"running","admins":{"checked":true,"unclaimed":[]},"workers":[{"name":"local","ready":true}]}`, true},
		{`{"phase":"workers-pending","admins":{"checked":true},"workers":[{"name":"local","ready":true}]}`, false},
		{`{"phase":"running","admins":{"checked":true,"unclaimed":["dev@loco.test"]},` +
			`"workers":[{"name":"local","ready":true}]}`, false},
		{`{"phase":"running","admins":{"checked":false},"workers":[{"name":"local","ready":true}]}`, false},
		{`{"phase":"running","admins":{"checked":true},"workers":[{"name":"local","ready":false}]}`, false},
		{`{"phase":"running","admins":{"checked":true},"workers":[]}`, false},
	} {
		got, err := running([]byte(tc.data))
		if err != nil || got != tc.want {
			t.Errorf("running(%s) = %v, %v", tc.data, got, err)
		}
	}
	if _, err := running([]byte("invalid")); err == nil {
		t.Fatal("accepted invalid status")
	}
}

func TestGatewayDNSPreservesFixturesAndIsIdempotent(t *testing.T) {
	const original = `.:53 {
    errors
    kubernetes cluster.local in-addr.arpa ip6.arpa {
      pods insecure
    }
    forward . /etc/resolv.conf
}
`
	corefile, err := gatewayCorefile(original, "loco-dev-2.localhost")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`^(api|ingest)\.loco-dev-2\.localhost$`, `^.+\.apps\.loco-dev-2\.localhost$`,
		"    kubernetes cluster.local", "    forward . /etc/resolv.conf",
	} {
		if !strings.Contains(corefile, want) {
			t.Errorf("missing %q in %s", want, corefile)
		}
	}
	repeat, err := gatewayCorefile(corefile, "loco-dev-2.localhost")
	if err != nil || repeat != corefile {
		t.Fatalf("repeat = %s, %v", repeat, err)
	}
	if _, err := gatewayCorefile("unexpected config", "loco-dev.localhost"); err == nil {
		t.Fatal("accepted missing Kubernetes plugin")
	}
}

func TestCLIEnvironmentUsesStackState(t *testing.T) {
	cfg := testConfig(t)
	env := cfg.cliEnv()
	for _, want := range []string{
		"HOME=" + filepath.Join(cfg.State, "home"), "LOCO_CREDENTIAL_STORE=file",
		"KUBECONFIG=" + filepath.Join(cfg.State, "home", ".kube", "config"),
	} {
		if !strings.Contains(strings.Join(env, "\n"), want) {
			t.Fatalf("environment missing %s", want)
		}
	}
}

func TestInstallationDecisionResetsResumesOrSkips(t *testing.T) {
	expected := stamp{ClusterUID: "new-cluster", Inputs: "current-inputs", Complete: true}
	for _, tc := range []struct {
		name  string
		saved stamp
		want  string
	}{
		{"fresh cluster", stamp{}, "reset"},
		{"recreated cluster", stamp{ClusterUID: "old-cluster", Inputs: "old-inputs", Complete: true}, "reset"},
		{"same cluster without completion stamp", stamp{ClusterUID: expected.ClusterUID, Inputs: expected.Inputs}, "resume"},
		{"completion stamp", expected, "skip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := installationAction(tc.saved, expected)
			if err != nil || got != tc.want {
				t.Fatalf("action = %s, %v; want %s", got, err, tc.want)
			}
		})
	}
	for _, complete := range []bool{false, true} {
		_, err := installationAction(
			stamp{ClusterUID: expected.ClusterUID, Inputs: "changed", Complete: complete},
			expected,
		)
		if err == nil || !strings.Contains(err.Error(), "run mise run tilt:reset") {
			t.Fatalf("changed inputs: %v", err)
		}
	}
}

func TestAPIHealthWaitsForGatewayAndHonorsCancellation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("health path = %s", r.URL.Path)
		}
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*readinessPoll)
	defer cancel()
	if err := waitAPI(ctx, server.URL+"/health"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("health requests = %d", requests.Load())
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if err := waitAPI(ctx, server.URL+"/health"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled health = %v", err)
	}
}

func TestNewClusterIntentRemovesPreviousReleaseBeforeRecordingCluster(t *testing.T) {
	cfg := testConfig(t)
	old := stamp{ClusterUID: "previous-cluster", Inputs: "previous-inputs", Complete: true}
	expected := stamp{ClusterUID: "current-cluster", Inputs: "current-inputs", Complete: true}
	if err := cfg.saveStamp(old); err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(cfg.State, "release.json")
	if err := os.Mkdir(releasePath, 0700); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(releasePath, "blocker")
	if err := os.WriteFile(blocker, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.beginInstallation(expected); err == nil {
		t.Fatal("expected release removal to fail")
	}
	data, err := os.ReadFile(filepath.Join(cfg.State, "install.stamp"))
	if err != nil {
		t.Fatal(err)
	}
	var got stamp
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != old {
		t.Fatalf("failed preparation changed stamp to %+v", got)
	}
	if err = os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(releasePath); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(releasePath, []byte(`{"version":"previous-release"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = cfg.beginInstallation(expected); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(releasePath); !os.IsNotExist(err) {
		t.Fatalf("release remains after new cluster intent: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(cfg.State, "install.stamp"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	expected.Complete = false
	if got != expected {
		t.Fatalf("pending stamp = %+v, want %+v", got, expected)
	}
}
