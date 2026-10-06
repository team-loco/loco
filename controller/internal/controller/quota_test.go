package controller

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestParseWorkspaceLimits(t *testing.T) {
	limits, err := parseWorkspaceLimits("", "", "")
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if limits.cpu.String() != defaultWorkspaceCPU || limits.memory.String() != defaultWorkspaceMemory {
		t.Fatalf(
			"defaults = %s/%s, want %s/%s",
			&limits.cpu,
			&limits.memory,
			defaultWorkspaceCPU,
			defaultWorkspaceMemory,
		)
	}

	limits, err = parseWorkspaceLimits("2", "4Gi", "10")
	if err != nil {
		t.Fatalf("explicit values: %v", err)
	}
	if limits.cpu.String() != "2" || limits.memory.String() != "4Gi" || limits.pods.String() != "10" {
		t.Fatalf("explicit values = %s/%s/%s", &limits.cpu, &limits.memory, &limits.pods)
	}

	if _, err := parseWorkspaceLimits("lots", "", ""); err == nil {
		t.Fatal("an invalid CPU quantity parsed")
	}
}

var _ = Describe("Workspace quota", func() {
	var reconciler *LocoResourceReconciler

	BeforeEach(func() {
		reconciler = &LocoResourceReconciler{Client: k8sClient, Scheme: scheme.Scheme}
	})

	It("applies the quota and container defaults to the workspace namespace", func() {
		app := isolationTestApplication("ws-quota", "quota")
		namespace := getNamespace(app)
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		Expect(reconciler.ensureWorkspaceQuota(ctx, app)).To(Succeed())
		Expect(reconciler.ensureWorkspaceQuota(ctx, app)).To(Succeed())

		quota := &corev1.ResourceQuota{}
		quotaKey := client.ObjectKey{Namespace: namespace, Name: workspaceQuotaName}
		Expect(k8sClient.Get(ctx, quotaKey, quota)).To(Succeed())
		hard := quota.Spec.Hard
		requestsCPU := hard[corev1.ResourceRequestsCPU]
		Expect(requestsCPU.String()).To(Equal(defaultWorkspaceCPU))
		limitsMemory := hard[corev1.ResourceLimitsMemory]
		Expect(limitsMemory.String()).To(Equal(defaultWorkspaceMemory))
		pods := hard[corev1.ResourcePods]
		Expect(pods.String()).To(Equal(defaultWorkspacePods))
		loadBalancers := hard[corev1.ResourceServicesLoadBalancers]
		Expect(loadBalancers.IsZero()).To(BeTrue())
		nodePorts := hard[corev1.ResourceServicesNodePorts]
		Expect(nodePorts.IsZero()).To(BeTrue())

		limitRange := &corev1.LimitRange{}
		limitRangeKey := client.ObjectKey{Namespace: namespace, Name: workspaceLimitRangeName}
		Expect(k8sClient.Get(ctx, limitRangeKey, limitRange)).To(Succeed())
		Expect(limitRange.Spec.Limits).To(HaveLen(1))
		Expect(limitRange.Spec.Limits[0].Type).To(Equal(corev1.LimitTypeContainer))
	})

	It("gives containers without resources the default requests and limits", func() {
		app := isolationTestApplication("ws-limitrange", "limitrange")
		namespace := getNamespace(app)
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		limitRange := workspaceLimitRange(app)
		Expect(k8sClient.Apply(ctx, limitRange, applyOptions()...)).To(Succeed())

		runAsNonRoot := true
		allowPrivilegeEscalation := false
		podSecurity := &corev1.PodSecurityContext{
			RunAsNonRoot:   &runAsNonRoot,
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		}
		containerSecurity := &corev1.SecurityContext{
			AllowPrivilegeEscalation: &allowPrivilegeEscalation,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "no-resources", Namespace: namespace},
			Spec: corev1.PodSpec{
				SecurityContext: podSecurity,
				Containers: []corev1.Container{{
					Name:            "app",
					Image:           "registry.example.com/app:latest",
					SecurityContext: containerSecurity,
				}},
			},
		}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())

		resources := pod.Spec.Containers[0].Resources
		Expect(resources.Requests.Cpu().String()).To(Equal(defaultContainerCPURequest))
		Expect(resources.Requests.Memory().String()).To(Equal(defaultContainerMemRequest))
		Expect(resources.Limits.Cpu().String()).To(Equal(defaultContainerCPULimit))
		Expect(resources.Limits.Memory().String()).To(Equal(defaultContainerMemLimit))
	})

	It("admits cluster-internal services and rejects load balancers once the quota is tracked", func() {
		app := isolationTestApplication("ws-services", "services")
		namespace := getNamespace(app)
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		Expect(reconciler.ensureWorkspaceQuota(ctx, app)).To(Succeed())

		quota := &corev1.ResourceQuota{}
		quotaKey := client.ObjectKey{Namespace: namespace, Name: workspaceQuotaName}
		Expect(k8sClient.Get(ctx, quotaKey, quota)).To(Succeed())
		used := corev1.ResourceList{}
		for name := range quota.Spec.Hard {
			used[name] = resource.MustParse("0")
		}
		quota.Status = corev1.ResourceQuotaStatus{Hard: quota.Spec.Hard, Used: used}
		Expect(k8sClient.Status().Update(ctx, quota)).To(Succeed())

		port := corev1.ServicePort{Port: 80}
		internal := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "internal", Namespace: namespace},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{port}},
		}
		Expect(k8sClient.Create(ctx, internal)).To(Succeed())

		external := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: namespace},
			Spec: corev1.ServiceSpec{
				Type:  corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{port},
			},
		}
		err := k8sClient.Create(ctx, external)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("exceeded quota"))
	})
})
