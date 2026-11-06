package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

const (
	testClusterUID   = "cluster-id"
	testInstallation = "example"
	testJSON         = "application/json"
	testNamespace    = "loco-system"
	testOriginal     = "original"
)

func TestSecretCreatePreservesOriginalMaterial(t *testing.T) {
	var stored *corev1.Secret
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", testJSON)
		switch r.Method {
		case http.MethodGet:
			if stored == nil {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`)
				return
			}
		case http.MethodPost:
			writes++
			if err := json.NewDecoder(r.Body).Decode(&stored); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s", r.Method)
		}
		if err := json.NewEncoder(w).Encode(stored); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	core, err := coreclient.NewForConfig(
		&rest.Config{Host: server.URL, ContentType: testJSON},
	)
	if err != nil {
		t.Fatal(err)
	}
	client := Kubernetes{Core: core, Target: KubernetesTarget{ClusterUID: testClusterUID, Namespace: testNamespace}}
	state := &corev1.ConfigMap{UID: types.UID("owner-id")}
	for _, value := range []string{testOriginal, "replacement"} {
		got, err := client.ensureSecret(
			t.Context(),
			state,
			"seal",
			[]string{secretKeyField},
			map[string][]byte{secretKeyField: []byte(value)},
		)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Data[secretKeyField]) != testOriginal {
			t.Fatal("existing material was replaced")
		}
	}
	if writes != 1 {
		t.Fatalf("creates=%d, want 1", writes)
	}
}

func TestSecretRejectsForeignAndMalformedExistingState(t *testing.T) {
	for _, test := range []struct {
		name, owner string
		data        map[string][]byte
		want        error
	}{
		{"foreign", "other", map[string][]byte{secretKeyField: []byte("private")}, errForeignOwnership},
		{"malformed", "owner-id", map[string][]byte{}, errSecretInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected write %s", r.Method)
				}
				w.Header().Set("Content-Type", testJSON)
				secret := corev1.Secret{
					Annotations: map[string]string{installationID: test.owner},
					Data:        test.data,
				}
				if err := json.NewEncoder(w).Encode(secret); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			core, err := coreclient.NewForConfig(
				&rest.Config{Host: server.URL, ContentType: testJSON},
			)
			if err != nil {
				t.Fatal(err)
			}
			client := Kubernetes{
				Core:   core,
				Target: KubernetesTarget{ClusterUID: testClusterUID, Namespace: testNamespace},
			}
			_, err = client.ensureSecret(
				t.Context(),
				&corev1.ConfigMap{UID: types.UID("owner-id")},
				"seal",
				[]string{secretKeyField},
				map[string][]byte{secretKeyField: []byte("replacement")},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestKubernetesErrorsDoNotExposeServerMessages(t *testing.T) {
	const canary = "credential-canary"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", testJSON)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(
			w,
			`{"kind":"Status","apiVersion":"v1","reason":"credential-canary","message":"`+canary+`","code":403}`,
		)
	}))
	defer server.Close()
	core, err := coreclient.NewForConfig(
		&rest.Config{Host: server.URL, ContentType: testJSON},
	)
	if err != nil {
		t.Fatal(err)
	}
	client := Kubernetes{Core: core, Target: KubernetesTarget{ClusterUID: testClusterUID, Namespace: testNamespace}}
	_, err = client.ReadBootstrapToken(t.Context(), testInstallation)
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestSecretRecoversLostCreateAcknowledgement(t *testing.T) {
	var stored *corev1.Secret
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", testJSON)
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&stored); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(
				w,
				`{"kind":"Status","apiVersion":"v1","reason":"InternalError","message":"credential-canary","code":500}`,
			)
			return
		}
		if stored == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`)
			return
		}
		if err := json.NewEncoder(w).Encode(stored); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	core, err := coreclient.NewForConfig(
		&rest.Config{Host: server.URL, ContentType: testJSON},
	)
	if err != nil {
		t.Fatal(err)
	}
	client := Kubernetes{Core: core, Target: KubernetesTarget{ClusterUID: testClusterUID, Namespace: testNamespace}}
	got, err := client.ensureSecret(
		t.Context(),
		&corev1.ConfigMap{UID: types.UID("owner-id")},
		"seal",
		[]string{secretKeyField},
		map[string][]byte{secretKeyField: []byte(testOriginal)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data[secretKeyField]) != testOriginal {
		t.Fatal("lost acknowledgment replaced credential")
	}
}

func TestBootstrapRejectsMalformedExistingToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", testJSON)
		if strings.HasSuffix(r.URL.Path, "/namespaces/kube-system") {
			if encodeErr := json.NewEncoder(w).
				Encode(&corev1.Namespace{UID: types.UID(testClusterUID)}); encodeErr != nil {
				t.Error(encodeErr)
			}
			return
		}
		if strings.Contains(r.URL.Path, "/configmaps/") {
			state := &corev1.ConfigMap{
				UID:    types.UID("state-id"),
				Labels: map[string]string{installationOwner: testInstallation},
			}
			if err := json.NewEncoder(w).Encode(state); err != nil {
				t.Error(err)
			}
			return
		}
		secret := &corev1.Secret{
			Annotations: map[string]string{installationID: "state-id"},
			Data:        map[string][]byte{BootstrapTokenKey: []byte("too-short")},
		}
		if err := json.NewEncoder(w).Encode(secret); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	core, err := coreclient.NewForConfig(
		&rest.Config{Host: server.URL, ContentType: testJSON},
	)
	if err != nil {
		t.Fatal(err)
	}
	kube := Kubernetes{Core: core, Target: KubernetesTarget{ClusterUID: testClusterUID, Namespace: testNamespace}}
	_, err = kube.ReadBootstrapToken(t.Context(), testInstallation)
	if !errors.Is(err, errSecretInvalid) {
		t.Fatalf("got %v, want invalid secret", err)
	}
}
