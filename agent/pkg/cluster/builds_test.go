package cluster

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBuildsEnabledFollowsTheServedBuildAPI(t *testing.T) {
	applications := metav1.APIResource{Name: "applications", Kind: "Application", Namespaced: true}
	builds := metav1.APIResource{Name: "builds", Kind: "Build", Namespaced: true}
	cases := []struct {
		name      string
		resources []*metav1.APIResourceList
		want      bool
	}{
		{name: "no loco api", resources: nil, want: false},
		{
			name: "applications only",
			resources: []*metav1.APIResourceList{{
				GroupVersion: "infra.loco.io/v1alpha1",
				APIResources: []metav1.APIResource{applications},
			}},
			want: false,
		},
		{
			name: "applications and builds",
			resources: []*metav1.APIResourceList{{
				GroupVersion: "infra.loco.io/v1alpha1",
				APIResources: []metav1.APIResource{applications, builds},
			}},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientset()
			discovery, ok := client.Discovery().(*fakediscovery.FakeDiscovery)
			if !ok {
				t.Fatal("fake clientset has no fake discovery")
			}
			discovery.Resources = tc.resources
			inspector := NewInspector(client, "loco-system", "loco-controller")

			got, err := inspector.BuildsEnabled(context.Background())
			if err != nil {
				t.Fatalf("BuildsEnabled: %v", err)
			}
			if got != tc.want {
				t.Fatalf("BuildsEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}
