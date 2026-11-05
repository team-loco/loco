package installation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

const testClusterUIDField = "clusterUID"

func TestLockCannotOverlapLiveHolderWithSkewedOperatorClock(t *testing.T) {
	serverTime := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	fixture := newClockLockFixture(t, serverTime, serverTime.Add(time.Minute), "valid")
	// This is a live holder's destination state when the contender's wall clock
	// runs an hour ahead. Its operation deadline has not expired.
	entered, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		close(entered)
		<-release
	}()
	<-entered
	defer func() { close(release); <-stopped }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	overlapped := false
	err := fixture.kube.withLock(ctx, fixture.state, func(context.Context, *corev1.ConfigMap) error {
		select {
		case <-stopped:
		default:
			overlapped = true
		}
		return nil
	})
	if err == nil || overlapped || fixture.mutations.Load() != 0 {
		t.Fatalf("live holder overlap=%v lock mutations=%d err=%v", overlapped, fixture.mutations.Load(), err)
	}
}

func TestLockRecoversExpiredLeaseUsingServerTime(t *testing.T) {
	serverTime := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	fixture := newClockLockFixture(t, serverTime, serverTime.Add(-time.Minute), "valid")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	ran := false
	if err := fixture.kube.withLock(ctx, fixture.state, func(context.Context, *corev1.ConfigMap) error {
		ran = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !ran || fixture.mutations.Load() != 2 {
		t.Fatalf("expired lease ran=%v mutations=%d", ran, fixture.mutations.Load())
	}
	expires, err := time.Parse(time.RFC3339, fixture.replacementExpiry)
	if err != nil || expires.Before(serverTime.Add(time.Minute+lockExpiryMargin-time.Second)) ||
		expires.After(serverTime.Add(time.Minute+lockExpiryMargin+time.Second)) {
		t.Fatalf("replacement expiry %q does not use server time: %v", fixture.replacementExpiry, err)
	}
}

func TestLockFailsClosedWhenServerClockCannotBeSampled(t *testing.T) {
	for _, sample := range []string{"forbidden", "missing"} {
		t.Run(sample, func(t *testing.T) {
			now := time.Now().UTC()
			fixture := newClockLockFixture(t, now, now.Add(-time.Hour), sample)
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			ran := false
			err := fixture.kube.withLock(ctx, fixture.state, func(context.Context, *corev1.ConfigMap) error {
				ran = true
				return nil
			})
			if err == nil || ran || fixture.mutations.Load() != 0 {
				t.Fatalf("clock unavailable: ran=%v mutations=%d err=%v", ran, fixture.mutations.Load(), err)
			}
		})
	}
}

type clockLockFixture struct {
	kube              Kubernetes
	state             *corev1.ConfigMap
	mutations         atomic.Int32
	replacementExpiry string
}

func newClockLockFixture(t *testing.T, serverTime, expires time.Time, sample string) *clockLockFixture {
	t.Helper()
	fixture := &clockLockFixture{state: &corev1.ConfigMap{
		UID: types.UID("state-id"), Labels: map[string]string{installationOwner: testInstallation},
		Data: map[string]string{testClusterUIDField: testClusterUID},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", testJSON)
		var response any
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("dryRun") == metav1.DryRunAll:
			if sample == "forbidden" {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"Forbidden","code":403}`)
				return
			}
			clock := &corev1.ConfigMap{}
			if sample != "missing" {
				clock.CreationTimestamp = metav1.NewTime(serverTime)
			}
			response = clock
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","reason":"AlreadyExists","code":409}`)
			return
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/loco-installation-lock"):
			response = &corev1.ConfigMap{
				UID: types.UID("lock-id"), ResourceVersion: "7",
				Annotations: map[string]string{installationID: string(fixture.state.UID)},
				Data:        map[string]string{"expires": expires.Format(time.RFC3339)},
			}
		case r.Method == http.MethodPut:
			var replacement corev1.ConfigMap
			if err := json.NewDecoder(r.Body).Decode(&replacement); err != nil {
				t.Error(err)
			}
			if replacement.ResourceVersion != "7" {
				t.Error("takeover lost its CAS resource version")
			}
			fixture.mutations.Add(1)
			fixture.replacementExpiry = replacement.Data["expires"]
			replacement.UID, replacement.ResourceVersion = types.UID("lock-id"), "8"
			response = &replacement
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/namespaces/kube-system"):
			response = &corev1.Namespace{UID: types.UID(testClusterUID)}
		case r.Method == http.MethodGet:
			response = fixture.state
		case r.Method == http.MethodDelete:
			var options metav1.DeleteOptions
			if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
				t.Error(err)
			}
			if options.Preconditions == nil || options.Preconditions.ResourceVersion == nil ||
				*options.Preconditions.ResourceVersion != "8" {
				t.Error("cleanup lost its acquired resource version")
			}
			fixture.mutations.Add(1)
			fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Success","code":200}`)
			return
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	client, err := coreclient.NewForConfig(&rest.Config{Host: server.URL, ContentType: testJSON})
	if err != nil {
		t.Fatal(err)
	}
	fixture.kube = Kubernetes{
		Core:   client,
		Target: KubernetesTarget{ClusterUID: testClusterUID, Namespace: testNamespace},
	}
	return fixture
}
