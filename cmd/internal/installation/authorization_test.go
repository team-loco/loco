package installation

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	authclient "k8s.io/client-go/kubernetes/typed/authorization/v1"
	"k8s.io/client-go/rest"
)

func TestOperatorRequiresClusterAdminWithoutCreatingGrant(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews") {
				t.Errorf("unexpected authorization write %s", r.URL.Path)
			}
			var review authorizationv1.SelfSubjectAccessReview
			if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
				t.Error(err)
			}
			attributes := review.Spec.ResourceAttributes
			if attributes == nil || attributes.Verb != "*" || attributes.Group != "*" || attributes.Resource != "*" {
				t.Error("review did not require cluster-admin access")
			}
			review.Status = authorizationv1.SubjectAccessReviewStatus{Allowed: allowed, Reason: "credential-canary"}
			w.Header().Set("Content-Type", testJSON)
			if err := json.NewEncoder(w).Encode(&review); err != nil {
				t.Error(err)
			}
		}))
		authorization, err := authclient.NewForConfig(
			&rest.Config{Host: server.URL, ContentType: testJSON},
		)
		if err != nil {
			t.Fatal(err)
		}
		kube := Kubernetes{Authorization: authorization}
		err = kube.requireClusterAdmin(t.Context())
		server.Close()
		if allowed && err != nil {
			t.Fatal(err)
		}
		if !allowed && !errors.Is(err, errClusterAdminRequired) {
			t.Fatalf("got %v, want cluster-admin required", err)
		}
		if err != nil && strings.Contains(err.Error(), "credential-canary") {
			t.Fatal("authorization error exposed server reason")
		}
	}
}
