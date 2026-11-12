package cluster

import (
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCheckKubernetesVersion(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    string
	}{
		{"v1.35.10", "upgrade the API server to 1.36"},
		{"v1.36.0-alpha.1", "unsupported"},
		{"v1.36.0", ""},
		{"v1.36.4+k3s1", ""},
		{"v1.37.0-eks-build", ""},
		{"v1.38.0", ""},
		{"invalid", "parse Kubernetes server version"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			client := fake.NewClientset()
			discovery, ok := client.Discovery().(*fakediscovery.FakeDiscovery)
			if !ok {
				t.Fatal("expected fake discovery")
			}
			discovery.FakedServerVersion = &version.Info{GitVersion: tc.version}
			err := NewInspector(client, "", "").CheckKubernetesVersion()
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCheckKubernetesVersionDiscoveryFailure(t *testing.T) {
	client := fake.NewClientset()
	failure := errors.New("API unavailable")
	client.PrependReactor("get", "version", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, failure
	})
	if err := NewInspector(client, "", "").CheckKubernetesVersion(); !errors.Is(err, failure) {
		t.Fatalf("error = %v, want %v", err, failure)
	}
}
