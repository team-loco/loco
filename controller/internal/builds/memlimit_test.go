package builds

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestBuilderContainersSetGoMemoryLimitBelowContainerLimit(t *testing.T) {
	limits := func(memory string) corev1.ResourceRequirements {
		return corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(memory)},
		}
	}
	r := &Reconciler{Config: Config{Resources: Resources{Fetch: limits("256Mi"), Push: limits("1000")}}}
	build := testBuild("memlimit")
	for _, tc := range []struct {
		container corev1.Container
		want      string
	}{
		{r.fetchContainer(build), "241591905"},
		{r.pushContainer(build), "900"},
	} {
		want := corev1.EnvVar{Name: "GOMEMLIMIT", Value: tc.want}
		if !slices.Contains(tc.container.Env, want) {
			t.Errorf("%s env %v, want %v", tc.container.Name, tc.container.Env, want)
		}
	}
}

func TestBuilderContainerWithoutMemoryLimitSetsNoGoMemoryLimit(t *testing.T) {
	r := &Reconciler{}
	if env := r.fetchContainer(
		testBuild("nolimit"),
	).Env; slices.ContainsFunc(
		env,
		func(e corev1.EnvVar) bool { return e.Name == "GOMEMLIMIT" },
	) {
		t.Errorf("fetch env %v sets GOMEMLIMIT", env)
	}
}
