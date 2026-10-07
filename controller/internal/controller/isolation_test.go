package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func isolationTestApplication(workspaceID, resourceID string) *locov1alpha1.Application {
	return &locov1alpha1.Application{
		Name: "isolation-" + resourceID, Namespace: testNamespace,
		Spec: locov1alpha1.ApplicationSpec{
			Type:          testAppType,
			ResourceID:    resourceID,
			WorkspaceID:   workspaceID,
			EnvironmentID: "env-production",
			Region:        "us-east-1",
			ServiceSpec: &locov1alpha1.ServiceSpec{
				Deployment: &locov1alpha1.ServiceDeploymentSpec{
					Image: testImage,
					Port:  8000,
				},
				Resources: &locov1alpha1.ResourcesSpec{
					CPU:      defaultCPURequest,
					Memory:   "64Mi",
					Replicas: locov1alpha1.ReplicasSpec{Min: 1, Max: 1},
				},
				Routing: &locov1alpha1.RoutingSpec{HostName: testHostName, PathPrefix: "/"},
			},
		},
	}
}

func policyResourceVersions(namespace string) map[string]string {
	list := &networkingv1.NetworkPolicyList{}
	Expect(k8sClient.List(ctx, list, client.InNamespace(namespace))).To(Succeed())
	versions := make(map[string]string, len(list.Items))
	for _, policy := range list.Items {
		versions[policy.Name] = policy.ResourceVersion
	}
	return versions
}

func getPolicy(namespace, name string) *networkingv1.NetworkPolicy {
	policy := &networkingv1.NetworkPolicy{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	Expect(k8sClient.Get(ctx, key, policy)).To(Succeed())
	return policy
}

var _ = Describe("Workspace isolation", func() {
	var reconciler *LocoResourceReconciler

	BeforeEach(func() {
		Expect(v1Gateway.Install(scheme.Scheme)).To(Succeed())
		reconciler = &LocoResourceReconciler{
			Client:        k8sClient,
			Scheme:        scheme.Scheme,
			locoNamespace: testLocoNamespace,
		}
	})

	It("labels the workspace namespace for restricted pod security, its workspace and environment", func() {
		app := isolationTestApplication("ws-labels", "labels")
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())

		ns := &corev1.Namespace{}
		key := client.ObjectKey{Name: getNamespace(app)}
		Expect(k8sClient.Get(ctx, key, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "restricted"))
		Expect(ns.Labels).To(HaveKeyWithValue("pod-security.kubernetes.io/audit", "restricted"))
		Expect(ns.Labels).To(HaveKeyWithValue("pod-security.kubernetes.io/warn", "restricted"))
		Expect(ns.Labels).To(HaveKeyWithValue(labelLocoApp, "true"))
		Expect(ns.Labels).To(HaveKeyWithValue(labelWorkspaceID, "ws-labels"))
		Expect(ns.Labels).To(HaveKeyWithValue(labelEnvironmentID, "env-production"))
		Expect(ns.Labels).NotTo(HaveKey(labelResourceID))
	})

	It("keeps labels it does not own on an existing namespace", func() {
		app := isolationTestApplication("ws-existing", "existing")
		existing := &corev1.Namespace{
			Name:   getNamespace(app),
			Labels: map[string]string{"unrelated": "kept"}}
		Expect(k8sClient.Create(ctx, existing)).To(Succeed())
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())

		ns := &corev1.Namespace{}
		key := client.ObjectKey{Name: getNamespace(app)}
		Expect(k8sClient.Get(ctx, key, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "restricted"))
		Expect(ns.Labels).To(HaveKeyWithValue("unrelated", "kept"))
	})

	It("creates the workspace policies once for every app in the workspace", func() {
		first := isolationTestApplication("ws-policies", "policies-first")
		second := isolationTestApplication("ws-policies", "policies-second")
		namespace := getNamespace(first)
		Expect(ensureNamespace(ctx, k8sClient, first)).To(Succeed())
		Expect(reconciler.ensureWorkspaceNetworkPolicies(ctx, first)).To(Succeed())

		versions := policyResourceVersions(namespace)
		Expect(versions).To(HaveLen(5))
		Expect(versions).To(HaveKey(policyDefaultDeny))
		Expect(versions).To(HaveKey(policyWorkspaceAccess))
		Expect(versions).To(HaveKey(policyDNSEgress))
		Expect(versions).To(HaveKey(policyTelemetryEgress))
		Expect(versions).To(HaveKey(policyInternetEgress))

		Expect(reconciler.ensureWorkspaceNetworkPolicies(ctx, second)).To(Succeed())
		Expect(reconciler.ensureWorkspaceNetworkPolicies(ctx, first)).To(Succeed())
		Expect(policyResourceVersions(namespace)).To(Equal(versions))

		dns := getPolicy(namespace, policyDNSEgress)
		Expect(dns.Spec.Egress).To(HaveLen(1))
		Expect(dns.Spec.Egress[0].To).To(BeEmpty())
		Expect(dns.Spec.Egress[0].Ports).To(HaveLen(2))
		Expect(dns.Spec.Egress[0].Ports[0].Port.IntValue()).To(Equal(53))

		access := getPolicy(namespace, policyWorkspaceAccess)
		Expect(access.Spec.PodSelector.MatchLabels).To(BeEmpty())
		Expect(access.Spec.Ingress).To(HaveLen(1))
		Expect(access.Spec.Ingress[0].Ports).To(BeEmpty())
		Expect(access.Spec.Ingress[0].From[0].NamespaceSelector).To(BeNil())
		Expect(access.Spec.Ingress[0].From[0].PodSelector).NotTo(BeNil())
		Expect(access.Spec.Egress[0].To[0].NamespaceSelector).To(BeNil())

		telemetry := getPolicy(namespace, policyTelemetryEgress)
		telemetryNamespace := telemetry.Spec.Egress[0].To[0].NamespaceSelector.MatchLabels
		Expect(telemetryNamespace).To(HaveKeyWithValue(labelNamespaceName, defaultObsNamespace))

		internet := getPolicy(namespace, policyInternetEgress)
		Expect(internet.Spec.Egress[0].To[0].IPBlock.Except).To(ContainElement("169.254.0.0/16"))
	})

	It("gives a routed app its own gateway policy and removes it with the route and the app", func() {
		app := isolationTestApplication("ws-gateway", "gateway")
		namespace := getNamespace(app)
		policyName := getGatewayPolicyName(app)
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		Expect(reconciler.ensureGatewayIngressPolicy(ctx, app)).To(Succeed())

		policy := getPolicy(namespace, policyName)
		Expect(policy.Spec.PodSelector.MatchLabels).To(Equal(map[string]string{labelApp: getName(app)}))
		Expect(policy.Spec.Ingress[0].Ports[0].Port.IntVal).To(Equal(int32(8000)))
		gatewayPods := policy.Spec.Ingress[0].From[0].PodSelector.MatchLabels
		Expect(gatewayPods).To(HaveKeyWithValue(labelGatewayName, gatewayName))
		gatewayNamespace := policy.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels
		Expect(gatewayNamespace).To(HaveKeyWithValue(labelNamespaceName, testLocoNamespace))

		app.Spec.ServiceSpec.Routing = nil
		Expect(reconciler.ensureGatewayIngressPolicy(ctx, app)).To(Succeed())
		key := client.ObjectKey{Namespace: namespace, Name: policyName}
		err := k8sClient.Get(ctx, key, &networkingv1.NetworkPolicy{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		app.Spec.ServiceSpec.Routing = &locov1alpha1.RoutingSpec{HostName: testHostName}
		Expect(reconciler.ensureGatewayIngressPolicy(ctx, app)).To(Succeed())
		Expect(reconciler.deleteAppObjects(ctx, app)).To(Succeed())
		err = k8sClient.Get(ctx, key, &networkingv1.NetworkPolicy{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("runs the app without a service account token under restricted pod security", func() {
		app := isolationTestApplication("ws-pods", "pods")
		namespace := getNamespace(app)
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		Expect(reconciler.ensureServiceAccount(ctx, app)).To(Succeed())
		_, err := reconciler.ensureDeployment(ctx, app, "1")
		Expect(err).NotTo(HaveOccurred())

		sa := &corev1.ServiceAccount{}
		appKey := client.ObjectKey{Namespace: namespace, Name: getName(app)}
		Expect(k8sClient.Get(ctx, appKey, sa)).To(Succeed())
		Expect(sa.AutomountServiceAccountToken).To(HaveValue(BeFalse()))

		deployment := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, appKey, deployment)).To(Succeed())
		Expect(deployment.Spec.Template.Spec.AutomountServiceAccountToken).To(HaveValue(BeFalse()))

		templateSpec := deployment.Spec.Template.Spec.DeepCopy()
		admitted := &corev1.Pod{
			Name: "from-template", Namespace: namespace,
			Spec: *templateSpec,
		}
		Expect(k8sClient.Create(ctx, admitted)).To(Succeed())

		rejected := admitted.DeepCopy()
		rejected.ObjectMeta = metav1.ObjectMeta{Name: "without-security-context", Namespace: namespace}
		rejected.Spec.SecurityContext = nil
		rejected.Spec.Containers[0].SecurityContext = nil
		err = k8sClient.Create(ctx, rejected)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`violates PodSecurity "restricted:latest"`))
	})
})
