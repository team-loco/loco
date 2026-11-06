package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

type clickHouseFixture struct {
	kube    Kubernetes
	state   *corev1.ConfigMap
	inputs  map[string][]byte
	config  managedClickHouse
	secrets map[string]*corev1.Secret
}

func newClickHouseFixture(t *testing.T) *clickHouseFixture {
	t.Helper()
	stored := map[string]*corev1.Secret{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", testJSON)
		if path.Base(r.URL.Path) == "pods" {
			if err := json.NewEncoder(w).Encode(&corev1.PodList{}); err != nil {
				t.Error(err)
			}
			return
		}
		if r.Method == http.MethodPost {
			var secret corev1.Secret
			if err := json.NewDecoder(r.Body).Decode(&secret); err != nil {
				t.Error(err)
			}
			stored[secret.Name] = &secret
			w.WriteHeader(http.StatusCreated)
			if err := json.NewEncoder(w).Encode(&secret); err != nil {
				t.Error(err)
			}
			return
		}
		secret, found := stored[path.Base(r.URL.Path)]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`)
			return
		}
		if err := json.NewEncoder(w).Encode(secret); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	core, err := coreclient.NewForConfig(&rest.Config{Host: server.URL, ContentType: testJSON, QPS: -1})
	if err != nil {
		t.Fatal(err)
	}
	kube := Kubernetes{Core: core, Target: KubernetesTarget{Namespace: testNamespace}}
	state := &corev1.ConfigMap{UID: types.UID("state-id"), Data: map[string]string{testBootstrapComplete: stateTrue}}
	inputs := map[string][]byte{}
	for _, key := range []string{envDatabaseURL, envSourceAccess, envSourceSecret, envRegistryUser, envRegistryPassword} {
		inputs[key] = []byte("fixture-input")
	}
	defaults, err := os.ReadFile(filepath.Join("..", "..", "..", "charts", "loco-control-plane", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	chart, err := packageArchive(map[string][]byte{controlPlaneValuesPath: defaults})
	if err != nil {
		t.Fatal(err)
	}
	config, err := readManagedClickHouse(chart)
	if err != nil {
		t.Fatal(err)
	}
	return &clickHouseFixture{kube: kube, state: state, inputs: inputs, config: config, secrets: stored}
}

func TestGeneratedClickHouseCredentialsUseManagedService(t *testing.T) {
	fixture := newClickHouseFixture(t)
	if err := fixture.kube.ensureCredentials(t.Context(), fixture.state, fixture.inputs, fixture.config); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"migrator", "ingest", "reader"} {
		secret := fixture.secrets["loco-clickhouse-"+role]
		endpoint, parseErr := url.Parse(string(secret.Data["url"]))
		if parseErr != nil {
			t.Fatal("invalid generated ClickHouse DSN")
		}
		if endpoint.Host != "clickhouse-loco-clickhouse.loco-system.svc.cluster.local:9000" {
			t.Errorf("%s credential does not address the chart's managed ClickHouse service", role)
		}
	}
}

func TestOperatorCredentialIsPreservedAcrossApply(t *testing.T) {
	fixture := newClickHouseFixture(t)
	if err := fixture.kube.ensureCredentials(t.Context(), fixture.state, fixture.inputs, fixture.config); err != nil {
		t.Fatal(err)
	}
	secret := fixture.secrets[fixture.config.OperatorName]
	if secret == nil || string(secret.Data[secretUsernameField]) != fixture.config.OperatorUsername ||
		len(secret.Data[secretPasswordField]) < minimumCredentialLength {
		t.Fatal("native operator credential was not created from pinned settings")
	}
	original := string(secret.Data[secretPasswordField])
	fixture.state.Data["credentialsEstablished"] = stateTrue
	if err := fixture.kube.ensureCredentials(t.Context(), fixture.state, fixture.inputs, fixture.config); err != nil {
		t.Fatal(err)
	}
	if string(fixture.secrets[fixture.config.OperatorName].Data[secretPasswordField]) != original {
		t.Fatal("reapply replaced the operator credential")
	}
}

func TestOperatorCredentialRequiresRecoveryInsteadOfRepair(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*clickHouseFixture)
		want   error
	}{
		{"deleted operator credential", func(f *clickHouseFixture) {
			delete(f.secrets, f.config.OperatorName)
		}, errRecoveryRequired},
		{"username", func(f *clickHouseFixture) {
			f.secrets[f.config.OperatorName].Data[secretUsernameField] = []byte("changed")
		}, errSecretInvalid},
		{"password", func(f *clickHouseFixture) {
			f.secrets[f.config.OperatorName].Data[secretPasswordField] = []byte("short")
		}, errSecretInvalid},
		{"foreign ownership", func(f *clickHouseFixture) {
			f.secrets[f.config.OperatorName].Annotations[installationID] = "alien-installation"
		}, errForeignOwnership},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newClickHouseFixture(t)
			if err := fixture.kube.ensureCredentials(
				t.Context(), fixture.state, fixture.inputs, fixture.config,
			); err != nil {
				t.Fatal(err)
			}
			fixture.state.Data["credentialsEstablished"] = stateTrue
			test.change(fixture)
			before, err := json.Marshal(fixture.secrets)
			if err != nil {
				t.Fatal(err)
			}
			err = fixture.kube.ensureCredentials(t.Context(), fixture.state, fixture.inputs, fixture.config)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			after, err := json.Marshal(fixture.secrets)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("reapply repaired or replaced existing credential material")
			}
		})
	}
}

func TestClickHouseNameComesFromPinnedChart(t *testing.T) {
	for _, name := range []string{"regional-observability", "", "credential-canary.invalid", strings.Repeat("a", 28)} {
		chart, err := packageArchive(map[string][]byte{
			controlPlaneValuesPath: fixtureClickHouseValues(name),
		})
		if err != nil {
			t.Fatal(err)
		}
		got, err := readManagedClickHouse(chart)
		if name == "regional-observability" {
			if err != nil || got.Name != name {
				t.Fatal("managed ClickHouse name did not follow the pinned chart")
			}
			continue
		}
		if err == nil || strings.Contains(err.Error(), "credential-canary") {
			t.Fatal("invalid naming metadata was accepted or exposed source data")
		}
	}
}

func TestManagedClickHouseNameFitsPinnedOperatorVolumes(t *testing.T) {
	// The pinned chart truncates cluster names to 15 characters and runs one shard/replica.
	cluster := strings.Repeat("c", 15)
	for _, size := range []int{26, 27} {
		name := strings.Repeat("a", size)
		chart, err := packageArchive(map[string][]byte{
			controlPlaneValuesPath: fixtureClickHouseValues(name),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = readManagedClickHouse(chart)
		valid := true
		for _, volume := range []string{
			"chi-" + name + "-common-configd",
			"chi-" + name + "-common-usersd",
			"chi-" + name + "-deploy-confd-" + cluster + "-0-0",
		} {
			valid = valid && len(volume) <= maxDNSLabelLength
		}
		if (err == nil) != valid {
			t.Errorf("name length %d: accepted=%v, generated volume names valid=%v", size, err == nil, valid)
		}
	}
}

func fixtureClickHouseValues(name string) []byte {
	return []byte(fmt.Sprintf("clickhouse:\n  fullnameOverride: %s\n  operator:\n"+
		"    fullnameOverride: fixture-clickhouse-operator\n    secret: {create: false, username: fixture_operator}\n", name))
}

func TestManagedClickHouseRequiresNativeOperatorCredentials(t *testing.T) {
	valid := string(fixtureClickHouseValues("fixture"))
	for _, invalid := range []string{
		strings.Replace(valid, "create: false, ", "", 1),
		strings.Replace(valid, "create: false", "create: true", 1),
		strings.Replace(valid, "fixture_operator", "", 1),
		strings.Replace(valid, "fixture-clickhouse-operator", "credential-canary.invalid", 1),
	} {
		chart, err := packageArchive(map[string][]byte{controlPlaneValuesPath: []byte(invalid)})
		if err != nil {
			t.Fatal(err)
		}
		_, err = readManagedClickHouse(chart)
		if !errors.Is(err, errInvalidRelease) || strings.Contains(err.Error(), "credential-canary") {
			t.Fatal("unsafe operator credential metadata was accepted or exposed")
		}
	}
}
