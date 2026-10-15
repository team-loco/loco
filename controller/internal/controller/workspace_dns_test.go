package controller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"
)

func chartDNSConfig() WorkspaceDNSConfig {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "charts", "loco-operator", "values.yaml"))
	Expect(err).NotTo(HaveOccurred())
	var values struct {
		WorkspaceDNS WorkspaceDNSConfig `json:"workspaceDNS"`
	}
	Expect(yaml.Unmarshal(data, &values)).To(Succeed())
	return values.WorkspaceDNS
}

var _ = Describe("Workspace DNS", func() {
	BeforeEach(func() { Expect(v1Gateway.Install(scheme.Scheme)).To(Succeed()) })
	It("puts applications on their workspace resolver without a bootstrap cycle", func() {
		app := isolationTestApplication("workspace-dns", "caller")
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		config := chartDNSConfig()
		r := &LocoResourceReconciler{Client: k8sClient, WorkspaceDNS: config}
		address, ready, err := r.ensureWorkspaceDNS(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		Expect(address).NotTo(BeEmpty())
		dep := &appsv1.Deployment{}
		key := client.ObjectKey{Namespace: getNamespace(app), Name: workspaceDNSName}
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		Expect(dep.Spec.Template.Spec.DNSPolicy).To(Equal(corev1.DNSClusterFirst))
		Expect(dep.Spec.Template.Spec.DNSConfig).To(BeNil())
		_, err = r.ensureDeployment(ctx, app, envSecretMount{})
		Expect(err).NotTo(HaveOccurred())
		key.Name = getName(app)
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		Expect(dep.Spec.Template.Spec.DNSPolicy).To(Equal(corev1.DNSNone))
		Expect(dep.Spec.Template.Spec.DNSConfig.Nameservers).To(Equal([]string{address}))
		Expect(
			dep.Spec.Template.Spec.DNSConfig.Searches[0],
		).To(Equal(getNamespace(app) + ".svc." + config.ClusterDomain))
	})
	It("waits for resolver readiness and shares it across application deletion", func() {
		app := isolationTestApplication("workspace-dns-readiness", "owner")
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		r := &LocoResourceReconciler{Client: k8sClient, WorkspaceDNS: chartDNSConfig()}
		_, ready, err := r.ensureWorkspaceDNS(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
		dep := &appsv1.Deployment{}
		key := client.ObjectKey{Name: workspaceDNSName, Namespace: getNamespace(app)}
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		dep.Status.Replicas = r.WorkspaceDNS.Replicas
		dep.Status.ReadyReplicas = r.WorkspaceDNS.Replicas
		dep.Status.ObservedGeneration = dep.Generation
		dep.Status.UpdatedReplicas = r.WorkspaceDNS.Replicas
		dep.Status.AvailableReplicas = r.WorkspaceDNS.Replicas
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
		_, ready, err = r.ensureWorkspaceDNS(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
		Expect(r.deleteAppObjects(ctx, app)).To(Succeed())
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		r.WorkspaceDNS.Replicas++
		_, ready, err = r.ensureWorkspaceDNS(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())
	})
	It("blocks application rollout while the workspace resolver is unavailable", func() {
		app := isolationTestApplication("workspace-dns-gate", "pending")
		r := &LocoResourceReconciler{Client: k8sClient, WorkspaceDNS: chartDNSConfig(), Telemetry: testTelemetry()}
		result, err := r.reconcileResources(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(deployingRequeue))
		Expect(app.Status.Phase).To(Equal(phaseDeploying))
		deployment := &appsv1.Deployment{}
		key := client.ObjectKey{Name: getName(app), Namespace: getNamespace(app)}
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, deployment))).To(BeTrue())
	})
	It("enqueues every application of the affected workspace", func() {
		first := isolationTestApplication("workspace-dns-watch", "first")
		second := isolationTestApplication("workspace-dns-watch", "second")
		other := isolationTestApplication("workspace-dns-watch-other", "other")
		for _, app := range []*locov1alpha1.Application{first, second, other} {
			Expect(k8sClient.Create(ctx, app)).To(Succeed())
		}
		r := &LocoResourceReconciler{Client: k8sClient}
		object := &corev1.Service{
			Labels: map[string]string{
				managed.LabelComponent:   componentWorkspaceDNS,
				managed.LabelWorkspaceID: first.Spec.WorkspaceID,
			},
		}
		requests := r.applicationsForManagedObject(ctx, object)
		Expect(requests).To(HaveLen(2))
		Expect(requests[0].Name).NotTo(Equal(other.Name))
		Expect(requests[1].Name).NotTo(Equal(other.Name))
	})

	It("rejects unmanaged infrastructure without changing it", func() {
		app := isolationTestApplication("workspace-dns-collision", "caller")
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		object := &corev1.ConfigMap{
			Name: workspaceDNSName, Namespace: getNamespace(app),
			Data: map[string]string{"Corefile": "operator-external-config"},
		}
		Expect(k8sClient.Create(ctx, object)).To(Succeed())
		r := &LocoResourceReconciler{Client: k8sClient, WorkspaceDNS: chartDNSConfig()}
		_, _, err := r.ensureWorkspaceDNS(ctx, app)
		Expect(err).To(MatchError(ContainSubstring(errWorkspaceDNSCollision.Error())))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
		Expect(object.Data["Corefile"]).To(Equal("operator-external-config"))
	})
	It("preserves allocated addresses and unchanged resources", func() {
		app := isolationTestApplication("workspace-dns-stable", "caller")
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		r := &LocoResourceReconciler{Client: k8sClient, WorkspaceDNS: chartDNSConfig()}
		address, _, err := r.ensureWorkspaceDNS(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		again, _, err := r.ensureWorkspaceDNS(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(address))
		dep := &appsv1.Deployment{}
		key := client.ObjectKey{Name: workspaceDNSName, Namespace: getNamespace(app)}
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		version := dep.ResourceVersion
		_, _, err = r.ensureWorkspaceDNS(ctx, app)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		Expect(dep.ResourceVersion).To(Equal(version))
	})
})

func TestWorkspaceDNSConfiguration(t *testing.T) {
	data, err := os.ReadFile("../../../charts/loco-operator/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		WorkspaceDNS WorkspaceDNSConfig `json:"workspaceDNS"`
	}
	if decodeErr := yaml.Unmarshal(data, &values); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	original := values.WorkspaceDNS
	cases := map[string]func(*WorkspaceDNSConfig){
		"image without digest":   func(c *WorkspaceDNSConfig) { c.Image = "coredns:latest" },
		"invalid cluster domain": func(c *WorkspaceDNSConfig) { c.ClusterDomain = "invalid/domain" },
		"missing domain":         func(c *WorkspaceDNSConfig) { c.ClusterDomain = "" },
		"zero replicas":          func(c *WorkspaceDNSConfig) { c.Replicas = 0 },
		"missing requests":       func(c *WorkspaceDNSConfig) { c.Resources.Requests = nil },
		"missing limits":         func(c *WorkspaceDNSConfig) { c.Resources.Limits = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			config := original
			mutate(&config)
			raw, marshalErr := json.Marshal(config)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if _, parseErr := ParseWorkspaceDNSConfig(string(raw)); parseErr == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}
