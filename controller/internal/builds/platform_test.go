package builds

import (
	"slices"
	"testing"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestBuildJobUsesNativeTargetPlatform(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := locov1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		r := &Reconciler{Scheme: scheme, Config: Config{NodeSelector: map[string]string{"loco.io/pool": "native"}}}
		build := testBuild("platform")
		build.Spec.Platform = "linux/" + arch
		job, err := r.buildJob(build)
		if err != nil {
			t.Fatal(err)
		}
		pod := job.Spec.Template.Spec
		if pod.NodeSelector[corev1.LabelArchStable] != arch || pod.NodeSelector[corev1.LabelOSStable] != "linux" ||
			pod.NodeSelector["loco.io/pool"] != "native" {
			t.Fatalf("native build selector %v", pod.NodeSelector)
		}
		if _, changed := r.Config.NodeSelector[corev1.LabelArchStable]; changed {
			t.Fatal("build modified the configured node selector")
		}
		if !slices.Contains(pod.InitContainers[1].Args, "--opt=platform="+build.Spec.Platform) {
			t.Fatalf("BuildKit args %v", pod.InitContainers[1].Args)
		}
		if !slices.Contains(pod.Containers[0].Args, "--platform="+build.Spec.Platform) {
			t.Fatalf("output validation args %v", pod.Containers[0].Args)
		}
	}
}
