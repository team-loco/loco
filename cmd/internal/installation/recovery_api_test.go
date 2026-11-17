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
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

const (
	sealSecretName      = "fixture-seal"
	controlPlaneRelease = "loco-control-plane"
	testNamespace       = "loco-system"
	testJSON            = "application/json"
)

func TestRecoveryExportReadsOnlyTheSelectedSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, sealName string
		malformed      bool
	}{
		{"static", sealSecretName, false},
		{"kms", "", false},
		{"invalid-shares", sealSecretName, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			custodian, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", testJSON)
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+sealSecretName):
					secret := &corev1.Secret{
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
					if tc.malformed {
						secret.Data["recovery-keys"] = []byte("invalid-base64")
					}

					if fixtureErr := json.NewEncoder(w).Encode(secret); fixtureErr != nil {
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
			kube := kubernetes{Core: client, Target: KubernetesTarget{Namespace: testNamespace}}
			err = kube.exportRecovery(
				t.Context(),
				tc.sealName, controlPlaneRelease+"-openbao-init",
				RecoveryExport{
					Output:          filepath.Join(t.TempDir(), "export"),
					Recipient:       owner.Recipient().String(),
					ShareRecipients: []string{custodian.Recipient().String()},
				},
			)
			if (err != nil) != tc.malformed {
				t.Fatalf("export error = %v", err)
			}
		})
	}
}
