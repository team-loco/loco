package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func isolationTestApplication(resourceID string) *locov1alpha1.Application {
	return &locov1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "isolation-" + resourceID},
		Spec: locov1alpha1.ApplicationSpec{
			Type:          "SERVICE",
			ResourceID:    resourceID,
			WorkspaceID:   "ws-a",
			EnvironmentID: "env-production",
			Region:        "us-east-1",
			ServiceSpec: &locov1alpha1.ServiceSpec{
				Deployment: &locov1alpha1.ServiceDeploymentSpec{
					Image: "registry.example.com/app:latest",
					Port:  8000,
				},
				Resources: &locov1alpha1.ResourcesSpec{
					CPU:      "50m",
					Memory:   "64Mi",
					Replicas: locov1alpha1.ReplicasSpec{Min: 1, Max: 1},
				},
				Routing: &locov1alpha1.RoutingSpec{HostName: "app.example.com", PathPrefix: "/"},
			},
		},
	}
}

var _ = Describe("Tenant isolation", func() {
	ctx := context.Background()
	reconciler := &LocoResourceReconciler{
		Client:        nil,
		Scheme:        scheme.Scheme,
		locoNamespace: "loco-system",
		obsNamespace:  defaultObsNamespace,
	}

	BeforeEach(func() {
		reconciler.Client = k8sClient
	})

	It("labels the namespace for restricted pod security and the workspace environment", func() {
		app := isolationTestApplication("labels")
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: getNamespace(app)}, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "restricted"))
		Expect(ns.Labels).To(HaveKeyWithValue("pod-security.kubernetes.io/audit", "restricted"))
		Expect(ns.Labels).To(HaveKeyWithValue("pod-security.kubernetes.io/warn", "restricted"))
		Expect(ns.Labels).To(HaveKeyWithValue(labelLocoApp, "true"))
		Expect(ns.Labels).To(HaveKeyWithValue(labelWorkspaceID, "ws-a"))
		Expect(ns.Labels).To(HaveKeyWithValue(labelEnvironmentID, "env-production"))
	})

	It("adds the pod security labels to a namespace created before them", func() {
		app := isolationTestApplication("existing")
		existing := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   getNamespace(app),
			Labels: map[string]string{"unrelated": "kept"},
		}}
		Expect(k8sClient.Create(ctx, existing)).To(Succeed())
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: getNamespace(app)}, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "restricted"))
		Expect(ns.Labels).To(HaveKeyWithValue("unrelated", "kept"))
	})

	It("creates the tenant network policies", func() {
		app := isolationTestApplication("policies")
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		Expect(reconciler.ensureNetworkPolicies(ctx, app)).To(Succeed())
		Expect(reconciler.ensureNetworkPolicies(ctx, app)).To(Succeed())

		list := &networkingv1.NetworkPolicyList{}
		Expect(k8sClient.List(ctx, list, client.InNamespace(getNamespace(app)))).To(Succeed())
		names := make([]string, 0, len(list.Items))
		for _, policy := range list.Items {
			names = append(names, policy.Name)
		}
		Expect(names).To(ConsistOf(
			policyDefaultDeny,
			policyGatewayIngress,
			policyEnvironmentAccess,
			policyDNSEgress,
			policyTelemetryEgress,
			policyInternetEgress,
		))

		access := &networkingv1.NetworkPolicy{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: getNamespace(app),
			Name:      policyEnvironmentAccess,
		}, access)).To(Succeed())
		peer := access.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels
		Expect(peer).To(HaveKeyWithValue(labelWorkspaceID, "ws-a"))
		Expect(peer).To(HaveKeyWithValue(labelEnvironmentID, "env-production"))

		internet := &networkingv1.NetworkPolicy{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: getNamespace(app),
			Name:      policyInternetEgress,
		}, internet)).To(Succeed())
		Expect(internet.Spec.Egress[0].To[0].IPBlock.Except).To(ContainElement("169.254.0.0/16"))
	})

	It("runs the application without a service account token under restricted pod security", func() {
		app := isolationTestApplication("pods")
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		Expect(reconciler.ensureServiceAccount(ctx, app)).To(Succeed())
		deployment, err := reconciler.ensureDeployment(ctx, app)
		Expect(err).NotTo(HaveOccurred())

		sa := &corev1.ServiceAccount{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: getNamespace(app), Name: getName(app)}, sa)).To(Succeed())
		Expect(sa.AutomountServiceAccountToken).To(HaveValue(BeFalse()))
		Expect(deployment.Spec.Template.Spec.AutomountServiceAccountToken).To(HaveValue(BeFalse()))

		admitted := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "from-template", Namespace: getNamespace(app)},
			Spec:       *deployment.Spec.Template.Spec.DeepCopy(),
		}
		Expect(k8sClient.Create(ctx, admitted)).To(Succeed())

		rejected := admitted.DeepCopy()
		rejected.ObjectMeta = metav1.ObjectMeta{Name: "without-security-context", Namespace: getNamespace(app)}
		rejected.Spec.SecurityContext = nil
		rejected.Spec.Containers[0].SecurityContext = nil
		err = k8sClient.Create(ctx, rejected)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("violates PodSecurity \"restricted:latest\""))
	})
})
