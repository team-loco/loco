package installation

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const testHTTPEmailTransport = "http://mail.test/invitations"
const testHTTPSEmailTransport = "https://mail.test/invitations"

func TestHTTPEmailInputsRequireAndReadTheProviderHookCredential(t *testing.T) {
	valid := "whsec_" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("h"), 32))
	for _, test := range []struct {
		name, transport, hook string
		valid                 bool
	}{
		{"http missing", testHTTPEmailTransport, "", false},
		{"https missing", testHTTPSEmailTransport, "", false},
		{"bad prefix", testHTTPEmailTransport, "credential-canary", false},
		{"bad encoding", testHTTPEmailTransport, "whsec_credential-canary", false},
		{"short key", testHTTPEmailTransport, "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 31)), false},
		{"long key", testHTTPEmailTransport, "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 33)), false},
		{"http", testHTTPEmailTransport, valid, true},
		{"https", testHTTPSEmailTransport, valid, true},
		{"smtp", "smtp://mail.test:587", "", true},
		{"smtp host missing", "smtp://:587", "", false},
		{"smtp port missing", "smtp://user:credential-canary@mail.test", "", false},
		{"unsupported scheme", "ftp://mail.test:587", "", false},
		{"http host missing", "http:///invitations", valid, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var reads atomic.Int32
			data := fixtureInputData("external-input-value")
			data["email-transport"] = []byte(test.transport)
			data["hook-secret"] = []byte(test.hook)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("input validation mutated cluster: %s", r.Method)
				}
				w.Header().Set("Content-Type", testJSON)
				if err := json.NewEncoder(w).Encode(&corev1.Secret{Data: data}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client, err := coreclient.NewForConfig(
				&rest.Config{Host: server.URL, ContentType: "application/json"},
			)
			if err != nil {
				t.Fatal(err)
			}
			kube := Kubernetes{Core: client, Target: KubernetesTarget{Namespace: testNamespace}}
			inputs, err := kube.inputs(t.Context(), Config{ControlPlane: fixtureInputReferences()})
			if (err == nil) != test.valid {
				t.Fatal("HTTP provider credential validation did not match the API contract")
			}
			if err != nil && strings.Contains(err.Error(), "credential-canary") {
				t.Fatal("input error exposes credentials")
			}
			if test.valid && string(inputs["EMAIL_HOOK_SECRET"]) != test.hook {
				t.Fatal("HTTP signing credential was not retained")
			}
			if reads.Load() != 1 {
				t.Fatal("provider credentials must come from one Secret version")
			}
		})
	}
}

func TestHTTPEmailCredentialReachesTheGeneratedAPIEnvironment(t *testing.T) {
	hook := []byte("whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32)))
	stored := map[string]*corev1.Secret{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", testJSON)
		switch {
		case strings.HasSuffix(r.URL.Path, "/pods"):
			if err := json.NewEncoder(w).Encode(&corev1.PodList{}); err != nil {
				t.Error(err)
			}
		case r.Method == http.MethodPost:
			var secret corev1.Secret
			if err := json.NewDecoder(r.Body).Decode(&secret); err != nil {
				t.Error(err)
				return
			}
			stored[secret.Name] = &secret
			if err := json.NewEncoder(w).Encode(&secret); err != nil {
				t.Error(err)
			}
		default:
			secret, found := stored[path.Base(r.URL.Path)]
			if !found {
				http.NotFound(w, r)
				return
			}
			if err := json.NewEncoder(w).Encode(secret); err != nil {
				t.Error(err)
			}
		}
	}))
	defer server.Close()
	client, err := coreclient.NewForConfig(
		&rest.Config{Host: server.URL, ContentType: "application/json"},
	)
	if err != nil {
		t.Fatal(err)
	}
	kube := Kubernetes{Core: client, Target: KubernetesTarget{Namespace: testNamespace}}
	inputs := map[string][]byte{
		envDatabaseURL: []byte(
			"postgres://database",
		),
		envSourceAccess:     []byte("access"),
		envSourceSecret:     []byte("source"),
		envRegistryUser:     []byte("registry"),
		envRegistryPassword: []byte("password"),
		"EMAIL_HOOK_SECRET": hook,
	}
	state := &corev1.ConfigMap{UID: types.UID("installation-id"), Data: map[string]string{}}
	if err = kube.ensureCredentials(t.Context(), state, inputs); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if stored["loco-api-env"] == nil || !bytes.Equal(stored["loco-api-env"].Data["EMAIL_HOOK_SECRET"], hook) {
		t.Fatal("generated API environment omits the provider HTTP signing credential")
	}
}

func TestHTTPEmailInputBlocksPlanAndApplyBeforeDestinationWrites(t *testing.T) {
	_, manifestPath, _ := fixtureReleaseRegistry(t)
	var mutations atomic.Int32
	data := fixtureInputData("external-input-value")
	data["email-transport"] = []byte(testHTTPEmailTransport)
	data["hook-secret"] = []byte("credential-canary")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", testJSON)
		if strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews") {
			fmt.Fprint(
				w,
				`{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","status":{"allowed":true}}`,
			)
			return
		}
		if r.Method != http.MethodGet {
			mutations.Add(1)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/namespaces/kube-system"):
			if err := json.NewEncoder(w).Encode(&corev1.Namespace{UID: types.UID(testClusterUID)}); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(r.URL.Path, "/secrets/"+testInputSecret):
			if err := json.NewEncoder(w).Encode(&corev1.Secret{Data: data}); err != nil {
				t.Error(err)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`)
		}
	}))
	defer server.Close()
	kubePath := filepath.Join(t.TempDir(), "kubeconfig")
	kubeConfig := clientcmdapi.Config{
		Clusters:  map[string]*clientcmdapi.Cluster{testFixture: {Server: server.URL}},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{testOperator: {}},
		Contexts:  map[string]*clientcmdapi.Context{testFixture: {Cluster: testFixture, AuthInfo: testOperator}},
	}
	if err := clientcmd.WriteToFile(kubeConfig, kubePath); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kubePath)
	cp := fixtureInputReferences()
	cp.Kubernetes = KubernetesTarget{ClusterUID: testClusterUID, Context: testFixture, Namespace: testNamespace}
	config := Config{Name: testInstallation, ControlPlane: cp}
	plan, err := PlanInstallation(t.Context(), config, manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Blocked, " "), errEmailHookSecret.Error()) {
		t.Fatal("plan did not report invalid HTTP email credentials")
	}
	if err = ApplyControlPlane(t.Context(), config, manifestPath); !errors.Is(err, errEmailHookSecret) {
		t.Fatal("apply accepted invalid HTTP email credentials")
	}
	if mutations.Load() != 0 {
		t.Fatal("invalid email credentials caused destination mutations")
	}
}
