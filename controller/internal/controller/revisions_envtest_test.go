package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var _ = Describe("Placement revisions", func() {
	var r *ApplicationReconciler
	newApp := func(name string) *locov1alpha1.Application {
		GinkgoHelper()
		app := workspaceApp(name, name)
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "1"}
		namespace := &corev1.Namespace{Name: testEnvironmentNamespace(app)}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, namespace)).To(Succeed()) })
		r = &ApplicationReconciler{
			Client: k8sClient, APIReader: k8sClient, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
			Telemetry: testTelemetry(),
		}
		return app
	}
	place := func(app *locov1alpha1.Application, revision string) *appsv1ac.DeploymentApplyConfiguration {
		GinkgoHelper()
		app.Annotations[locov1alpha1.KeyPlacementRevision] = revision
		dep, err := r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		return dep
	}

	It("rolls the pod template on a restart, not on a placement revision alone", func() {
		app := newApp("restart")
		place(app, "1")
		before := appDeployment(app)
		Expect(before.Spec.Template.Annotations).NotTo(HaveKey(locov1alpha1.KeyPlacementRevision))

		place(app, "2")
		Expect(appDeployment(app).Generation).To(Equal(before.Generation))

		app.Spec.RestartedAt = "2026-11-20T10:00:00.5Z"
		place(app, "3")
		after := appDeployment(app)
		Expect(after.Generation).To(Equal(before.Generation + 1))
		Expect(after.Spec.Template.Annotations).
			To(HaveKeyWithValue(locolabels.AnnotationRestartedAt, app.Spec.RestartedAt))
		Expect(after.Spec.Template.Spec).To(Equal(before.Spec.Template.Spec))
	})

	It("moves the revision to the ReplicaSet that runs the template and leaves its pods alone", func() {
		app := newApp("returning")
		observe := func(dep *appsv1ac.DeploymentApplyConfiguration) map[string]int64 {
			GinkgoHelper()
			Expect(r.observeReplicas(ctx, app, dep, time.Now())).To(Succeed())
			revisions := map[string]int64{}
			for _, replica := range app.Status.Rollout.Replicas {
				revisions[replica.UID] = replica.PlacementRevision
			}
			return revisions
		}

		first, firstPod := templateReplicaSet(app, place(app, "1"), "first")
		Expect(observe(place(app, "1"))).To(Equal(map[string]int64{string(firstPod.UID): 1}))

		app.Spec.ServiceSpec.Deployment.Image = "registry.example.com/app:v2"
		_, secondPod := templateReplicaSet(app, place(app, "2"), "second")
		Expect(observe(place(app, "2"))).To(Equal(map[string]int64{string(firstPod.UID): 1, string(secondPod.UID): 2}))

		app.Spec.ServiceSpec.Deployment.Image = testImage
		returned := place(app, "3")
		want := map[string]int64{string(firstPod.UID): 3, string(secondPod.UID): 2}
		Expect(observe(returned)).To(Equal(want))
		stored := &appsv1.ReplicaSet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(first), stored)).To(Succeed())
		Expect(stored.Annotations).To(HaveKeyWithValue(locov1alpha1.KeyPlacementRevision, "3"))
		Expect(stored.Spec.Template).To(Equal(first.Spec.Template))

		Expect(observe(returned)).To(Equal(want))
		again := &appsv1.ReplicaSet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(first), again)).To(Succeed())
		Expect(again.ResourceVersion).To(Equal(stored.ResourceVersion))
	})
})

// templateReplicaSet stands in for the Deployment controller: it creates a ReplicaSet running the
// applied Deployment's template and one pod it owns.
func templateReplicaSet(
	app *locov1alpha1.Application,
	dep *appsv1ac.DeploymentApplyConfiguration,
	name string,
) (*appsv1.ReplicaSet, *corev1.Pod) {
	GinkgoHelper()
	stored := appDeployment(app)
	Expect(stored.Generation).To(Equal(*dep.Generation))
	template := *stored.Spec.Template.DeepCopy()
	template.Labels[appsv1.DefaultDeploymentUniqueLabelKey] = name
	set := &appsv1.ReplicaSet{
		Name: testAppName(app) + "-" + name, Namespace: stored.Namespace, Labels: template.Labels,
		Spec: appsv1.ReplicaSetSpec{Replicas: new(int32(1)), Selector: stored.Spec.Selector, Template: template},
	}
	Expect(k8sClient.Create(ctx, set)).To(Succeed())
	pod := &corev1.Pod{
		Name: set.Name + "-pod", Namespace: set.Namespace, Labels: template.Labels, Annotations: template.Annotations,
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(set, appsv1.SchemeGroupVersion.WithKind("ReplicaSet")),
		},
		Spec: template.Spec,
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	return set, pod
}
