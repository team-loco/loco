package installation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

func TestLockRefreshesStateAndProtectsReplacementHolder(t *testing.T) {
	for _, takeover := range []bool{false, true} {
		t.Run(strconv.FormatBool(takeover), func(t *testing.T) {
			deleted := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", testJSON)
				switch {
				case r.Method == http.MethodPost && r.URL.Query().Get("dryRun") == metav1.DryRunAll:
					if err := json.NewEncoder(w).
						Encode(&corev1.ConfigMap{CreationTimestamp: metav1.Now()}); err != nil {
						t.Error(err)
					}
				case r.Method == http.MethodPost:
					var lock corev1.ConfigMap
					if err := json.NewDecoder(r.Body).Decode(&lock); err != nil {
						t.Error(err)
					}
					lock.UID = types.UID("lock-id")
					lock.ResourceVersion = "1"
					if err := json.NewEncoder(w).Encode(&lock); err != nil {
						t.Error(err)
					}
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/namespaces/kube-system"):
					if err := json.NewEncoder(w).
						Encode(&corev1.Namespace{UID: types.UID(testClusterUID)}); err != nil {
						t.Error(err)
					}
				case r.Method == http.MethodGet:
					state := corev1.ConfigMap{
						UID:    types.UID("state-id"),
						Labels: map[string]string{installationOwner: testInstallation},
						Data:   map[string]string{"clusterUID": testClusterUID, testBootstrapComplete: stateTrue},
					}
					if err := json.NewEncoder(w).Encode(&state); err != nil {
						t.Error(err)
					}
				case r.Method == http.MethodDelete:
					var options metav1.DeleteOptions
					if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
						t.Error(err)
					}
					if options.Preconditions == nil || options.Preconditions.ResourceVersion == nil ||
						*options.Preconditions.ResourceVersion != "1" {
						t.Error("cleanup has no acquired resource-version precondition")
					}
					if takeover {
						w.WriteHeader(http.StatusConflict)
						fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"Conflict","code":409}`)
						return
					}
					deleted = true
					fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Success","code":200}`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
			}))
			defer server.Close()
			core, err := coreclient.NewForConfig(
				&rest.Config{Host: server.URL, ContentType: testJSON},
			)
			if err != nil {
				t.Fatal(err)
			}
			kube := Kubernetes{
				Core:   core,
				Target: KubernetesTarget{ClusterUID: testClusterUID, Namespace: testNamespace},
			}
			stale := &corev1.ConfigMap{
				UID:    types.UID("state-id"),
				Labels: map[string]string{installationOwner: testInstallation},
				Data:   map[string]string{"bootstrapComplete": "false"},
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			err = kube.withLock(ctx, stale, func(_ context.Context, fresh *corev1.ConfigMap) error {
				if fresh.Data["bootstrapComplete"] != stateTrue {
					t.Fatal("lock callback received stale state")
				}
				return nil
			})
			if takeover && err == nil {
				t.Fatal("replacement lock cleanup succeeded")
			}
			if !takeover && err != nil {
				t.Fatal(err)
			}
			if deleted == takeover {
				t.Fatal("lock ownership was not preserved")
			}
		})
	}
}
