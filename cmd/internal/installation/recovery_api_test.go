package installation

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

func TestRecoveryExportPreservesSourceAndRecordsOnlyCompletedExport(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign-seal", true: "owned-seal"}[valid], func(t *testing.T) {
			owner, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			custodian, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			recorded := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", testJSON)
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+sealSecretName):
					identity := "foreign"
					if valid {
						identity = "fixture-installation"
					}
					secret := &corev1.Secret{
						Annotations: map[string]string{installationID: identity},
						Data: map[string][]byte{
							secretKeyField: []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))),
						},
					}
					if fixtureErr := json.NewEncoder(w).Encode(secret); fixtureErr != nil {
						t.Error(fixtureErr)
					}

				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "-openbao-init"):
					secret := &corev1.Secret{Data: map[string][]byte{
						"recovery-keys":       []byte(base64.StdEncoding.EncodeToString([]byte(firstShareCanary))),
						"generate-root-token": []byte("token-canary"),
					}}
					if fixtureErr := json.NewEncoder(w).Encode(secret); fixtureErr != nil {
						t.Error(fixtureErr)
					}

				case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/configmaps/"):
					var state corev1.ConfigMap
					if fixtureErr := json.NewDecoder(r.Body).Decode(&state); fixtureErr != nil {
						t.Error(fixtureErr)
					}
					recorded = state.Data["recoveryExported"] == stateTrue
					if fixtureErr := json.NewEncoder(w).Encode(&state); fixtureErr != nil {
						t.Error(fixtureErr)
					}
				default:
					t.Errorf("export mutated source: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusForbidden)
				}
			}))
			defer server.Close()
			client, err := coreclient.NewForConfig(&rest.Config{Host: server.URL, ContentType: testJSON})
			if err != nil {
				t.Fatal(err)
			}
			kube := Kubernetes{Core: client, Target: KubernetesTarget{Namespace: testNamespace}}
			err = kube.exportRecovery(
				t.Context(),
				&corev1.ConfigMap{
					Name: installationState,
					UID:  types.UID("fixture-installation"),
					Data: map[string]string{},
				},
				RecoveryExport{
					Output:          filepath.Join(t.TempDir(), "export"),
					Recipient:       owner.Recipient().String(),
					ShareRecipients: []string{custodian.Recipient().String()},
				},
			)
			if (err == nil) != valid || recorded != valid {
				t.Fatalf("err=%v recorded=%v", err, recorded)
			}
		})
	}
}
