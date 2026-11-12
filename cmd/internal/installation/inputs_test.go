package installation

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

const testInputSecret = "inputs"
const testEmailTransportKey = "email-transport"

func TestInputCredentialsUseOneSecretVersionPerOperation(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		generation := fmt.Sprintf("generation-%d", reads.Add(1))

		w.Header().Set("Content-Type", testJSON)
		if err := json.NewEncoder(w).Encode(&corev1.Secret{Data: fixtureInputData(generation)}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client, err := coreclient.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	kube := Kubernetes{Core: client, Target: KubernetesTarget{Namespace: testNamespace}}
	for operation := 1; operation <= 2; operation++ {
		inputs, inputErr := kube.inputs(t.Context(), Config{ControlPlane: fixtureInputReferences()})
		if inputErr != nil {
			t.Fatal(inputErr)
		}
		for key, value := range inputs {
			want := fmt.Sprintf("generation-%d", operation)
			if key == testEmailTransportKey {
				want = "https://" + want
			}
			if key == envEmailHookSecret {
				want = string(fixtureInputData(want)[emailHookSecretKey])
			}
			if string(value) != want {
				t.Errorf("operation %d input %s = %q, want %s", operation, key, value, want)
			}
		}
	}
	if got := reads.Load(); got != 2 {
		t.Errorf("read Secret %d times, want one snapshot per operation", got)
	}
}

func fixtureInputReferences() ControlPlane {
	return ControlPlane{
		Database:     Database{SecretRef: SecretKeyRef{Name: testInputSecret, Key: "database-url"}},
		SourceBucket: Bucket{CredentialsSecretRef: SecretRef{Name: testInputSecret}},
		Registry:     Registry{CredentialsSecretRef: SecretRef{Name: testInputSecret}},
		Email:        Email{TransportSecretRef: SecretKeyRef{Name: testInputSecret, Key: testEmailTransportKey}},
		OpenBao:      OpenBao{Snapshots: Bucket{CredentialsSecretRef: SecretRef{Name: testInputSecret}}},
	}
}

func fixtureInputData(value string) map[string][]byte {
	data := map[string][]byte{}
	for _, key := range []string{
		"database-url", "access-key-id", "secret-access-key", "api-username", "api-password",
		"push-username", "push-password", "pull-username", "pull-password", testEmailTransportKey,
	} {
		data[key] = []byte(value)
	}
	data[testEmailTransportKey] = []byte("https://" + value)
	key := sha256.Sum256([]byte(value))
	data[emailHookSecretKey] = []byte("whsec_" + base64.StdEncoding.EncodeToString(key[:]))
	return data
}
