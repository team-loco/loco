package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

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
		PrivateEgress: `[{"cidr":"172.18.0.0/16","port":5201}]`,
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

func TestRunningRequiresAnEnrolledWorkerAndClaimedAdmin(t *testing.T) {
	for _, tc := range []struct {
		data string
		want bool
	}{
		{`{"unclaimedAdmins":[],"clusters":[{"cluster":{"name":"local"},"enrollment":"enrolled"}]}`, true},
		{`{"unclaimedAdmins":["dev@loco.test"],"clusters":[{"cluster":{"name":"local"},"enrollment":"enrolled"}]}`, false},
		{`{"unclaimedAdmins":[],"clusters":[{"cluster":{"name":"local"},"enrollment":"pending"}]}`, false},
		{`{"unclaimedAdmins":[],"clusters":[]}`, false},
	} {
		got, err := running([]byte(tc.data))
		if err != nil || got != tc.want {
			t.Fatalf("running=%v,%v", got, err)
		}
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
		`^(api|ingest)\.loco-dev-2\.localhost\.$`, `^.+\.apps\.loco-dev-2\.localhost\.$`,
		"    kubernetes cluster.local", "    forward . /etc/resolv.conf",
	} {
		if !strings.Contains(corefile, want) {
			t.Errorf("missing %q in %s", want, corefile)
		}
	}
	for line := range strings.SplitSeq(corefile, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 4 && fields[0] == "rewrite" {
			pattern, compileErr := regexp.Compile(fields[4])
			if compileErr != nil {
				t.Fatal(compileErr)
			}
			question := "api.loco-dev-2.localhost."
			if strings.Contains(fields[4], "apps") {
				question = "service.apps.loco-dev-2.localhost."
			}
			if !pattern.MatchString(question) || pattern.MatchString(question+"other.") {
				t.Errorf("pattern %s does not isolate DNS question %s", fields[4], question)
			}
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

func TestRegistryConfigPreservesImageCompatibilityAndAnonymousArtifacts(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.registryConfig(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cfg.State, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		HTTP struct {
			Port          string   `json:"port"`
			Compat        []string `json:"compat"`
			AccessControl struct {
				Repositories map[string]struct {
					AnonymousPolicy []string `json:"anonymousPolicy"`
				} `json:"repositories"`
			} `json:"accessControl"`
		} `json:"http"`
	}
	if err = json.Unmarshal(data, &registry); err != nil {
		t.Fatal(err)
	}
	if registry.HTTP.Port != cfg.RegistryPort || len(registry.HTTP.Compat) != 1 ||
		registry.HTTP.Compat[0] != "docker2s2" ||
		strings.Join(registry.HTTP.AccessControl.Repositories["team-loco/**"].AnonymousPolicy, ",") != "read" {
		t.Fatalf("registry: %+v", registry)
	}
}

func TestEnrollmentResumesAnExistingRegistration(t *testing.T) {
	args, err := enrollmentArgs([]byte(`{"clusters":[{"id":"pending-id","name":"local"}]}`), "kind-worker")
	if err != nil || strings.Join(args, " ") != "cluster join-token pending-id --kube-context kind-worker" {
		t.Fatalf("enrollment=%v,%v", args, err)
	}
	args, err = enrollmentArgs([]byte(`{"clusters":[]}`), "kind-worker")
	if err != nil ||
		strings.Join(
			args,
			" ",
		) != "cluster create --name local --region local --tier production --kube-context kind-worker" {
		t.Fatalf("new enrollment=%v,%v", args, err)
	}
}

func TestInvalidArgumentsFailBeforeStartingTheStack(t *testing.T) {
	for _, args := range [][]string{
		nil, {"install", "trailing"}, {"ca", "unexpected"}, {"deploy", "--suspned"},
		{"deploy", "--suspend", "extra"}, {"unknown"},
	} {
		if err := run(t.Context(), config{}, args); err == nil {
			t.Fatalf("accepted unsupported arguments: %v", args)
		}
	}
}

func TestDashboardUsesTheGatewayAPIHostname(t *testing.T) {
	cfg := testConfig(t)
	cmd := exec.CommandContext(t.Context(), "bash", "-c", `source dev/stack.sh; printf '%s' "$PUBLIC_API_URL"`)
	cmd.Env = append(os.Environ(), "LOCO_STACK_SLOT=2")
	output, err := cmd.Output()
	if err != nil || string(output) != cfg.apiURL() {
		t.Fatalf("dashboard API URL = %q, %v; want %q", output, err, cfg.apiURL())
	}
}
