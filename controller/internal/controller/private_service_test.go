package controller

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	testPrivateHTTPName = "cart"
	testPrivateTCPName  = "payment"
)

var _ = Describe("Private service discovery", func() {
	BeforeEach(func() { Expect(v1Gateway.Install(scheme.Scheme)).To(Succeed()) })
	It("exposes a logical TCP endpoint alongside the UUID HTTP routing endpoint", func() {
		app := isolationTestApplication("discovery", testPrivateHTTPName)
		Expect(json.Unmarshal([]byte(`{"resourceName":"cart"}`), &app.Spec)).To(Succeed())
		app.Spec.ServiceSpec.Deployment.Port = 7070
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		reconciler := &LocoResourceReconciler{Client: k8sClient}
		Expect(reconciler.ensureService(ctx, app)).To(Succeed())
		alias := &corev1.Service{}
		Expect(
			k8sClient.Get(ctx, client.ObjectKey{Namespace: getNamespace(app), Name: testPrivateHTTPName}, alias),
		).To(Succeed())
		Expect(alias.Spec.Ports).To(HaveLen(1))
		Expect(alias.Spec.Ports[0].Port).To(Equal(int32(7070)))
		Expect(alias.Spec.Ports[0].TargetPort.IntVal).To(Equal(int32(7070)))
		Expect(alias.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))
		Expect(alias.Spec.Selector).To(Equal(map[string]string{labelApp: getName(app)}))
		legacy := &corev1.Service{}
		Expect(
			k8sClient.Get(ctx, client.ObjectKey{Namespace: getNamespace(app), Name: getName(app)}, legacy),
		).To(Succeed())
		Expect(legacy.Spec.Ports[0].Port).To(Equal(servicePort))
	})
	It("keeps the ClusterIP on port changes and removes only the renamed alias", func() {
		app := isolationTestApplication("discovery-rename", "rename")
		app.Spec.ResourceName = testPrivateTCPName
		app.Spec.ServiceSpec.Deployment.Port = 50051
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		r := &LocoResourceReconciler{Client: k8sClient}
		Expect(r.ensureService(ctx, app)).To(Succeed())
		key := client.ObjectKey{Namespace: getNamespace(app), Name: testPrivateTCPName}
		svc := &corev1.Service{}
		Expect(k8sClient.Get(ctx, key, svc)).To(Succeed())
		address, version := svc.Spec.ClusterIP, svc.ResourceVersion
		Expect(r.ensureService(ctx, app)).To(Succeed())
		Expect(k8sClient.Get(ctx, key, svc)).To(Succeed())
		Expect(svc.ResourceVersion).To(Equal(version))
		app.Spec.ServiceSpec.Deployment.Port = 3550
		Expect(r.ensureService(ctx, app)).To(Succeed())
		Expect(k8sClient.Get(ctx, key, svc)).To(Succeed())
		Expect(svc.Spec.ClusterIP).To(Equal(address))
		Expect(svc.Spec.Ports[0].Port).To(Equal(int32(3550)))
		Expect(svc.Spec.Ports[0].TargetPort.IntVal).To(Equal(int32(3550)))
		app.Spec.ResourceName = "product-catalog"
		Expect(r.ensureService(ctx, app)).To(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, svc))).To(BeTrue())
		key.Name = "product-catalog"
		Expect(k8sClient.Get(ctx, key, svc)).To(Succeed())
		Expect(r.deleteAppObjects(ctx, app)).To(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, svc))).To(BeTrue())
	})

	It("waits for the previous owner to release a reused name and protects the replacement", func() {
		old := isolationTestApplication("discovery-replacement", "old-owner")
		old.Spec.ResourceName = testPrivateHTTPName
		replacement := isolationTestApplication("discovery-replacement", "new-owner")
		replacement.Spec.ResourceName = testPrivateHTTPName
		Expect(k8sClient.Create(ctx, old)).To(Succeed())
		Expect(k8sClient.Create(ctx, replacement)).To(Succeed())
		Expect(ensureNamespace(ctx, k8sClient, old)).To(Succeed())
		r := &LocoResourceReconciler{Client: k8sClient}
		Expect(r.ensureService(ctx, old)).To(Succeed())
		Expect(r.ensureService(ctx, replacement)).To(MatchError(ContainSubstring(errPrivateServiceCollision.Error())))
		Expect(r.deleteAppObjects(ctx, old)).To(Succeed())
		Expect(r.ensureService(ctx, replacement)).To(Succeed())
		Expect(r.prunePrivateServices(ctx, old, "")).To(Succeed())
		svc := &corev1.Service{}
		key := client.ObjectKey{Namespace: getNamespace(replacement), Name: testPrivateHTTPName}
		Expect(k8sClient.Get(ctx, key, svc)).To(Succeed())
		Expect(ownsPrivateService(replacement, svc)).To(BeTrue())
		Expect(svc.Spec.Selector).To(Equal(map[string]string{labelApp: getName(replacement)}))
	})

	It("refuses to adopt an unmanaged name collision", func() {
		app := isolationTestApplication("discovery-collision", "collision")
		app.Spec.ResourceName = testPrivateHTTPName
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		existing := &corev1.Service{Name: testPrivateHTTPName, Namespace: getNamespace(app), Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 7070}}, Selector: map[string]string{"unmanaged": "true"},
		}}
		Expect(k8sClient.Create(ctx, existing)).To(Succeed())
		version := existing.ResourceVersion
		r := &LocoResourceReconciler{Client: k8sClient}
		Expect(r.ensureService(ctx, app)).To(MatchError(ContainSubstring(errPrivateServiceCollision.Error())))
		Expect(r.prunePrivateServices(ctx, app, "")).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(existing), existing)).To(Succeed())
		Expect(existing.ResourceVersion).To(Equal(version))
		Expect(existing.Spec.Selector).To(Equal(map[string]string{"unmanaged": "true"}))
	})

	It("scopes identical logical names to separate workspaces", func() {
		r := &LocoResourceReconciler{Client: k8sClient}
		for _, workspace := range []string{"discovery-first", "discovery-second"} {
			app := isolationTestApplication(workspace, workspace)
			app.Spec.ResourceName = testPrivateTCPName
			Expect(k8sClient.Create(ctx, app)).To(Succeed())
			Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
			Expect(r.ensureService(ctx, app)).To(Succeed())
			svc := &corev1.Service{}
			key := client.ObjectKey{Namespace: getNamespace(app), Name: testPrivateTCPName}
			Expect(k8sClient.Get(ctx, key, svc)).To(Succeed())
			Expect(ownsPrivateService(app, svc)).To(BeTrue())
		}
	})

	It("supports digit-leading names on the pinned API server", func() {
		app := isolationTestApplication("discovery-digit", "digit")
		app.Spec.ResourceName = "123cart"
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
		r := &LocoResourceReconciler{Client: k8sClient}
		Expect(r.ensureService(ctx, app)).To(Succeed())
	})

	It("keeps unsafe legacy resource names on their UUID endpoints", func() {
		for _, name := range []string{"Legacy_API", "localhost", "resource-01234567-89ab-cdef-0123-456789abcdef"} {
			app := isolationTestApplication("discovery-legacy", "legacy")
			app.Spec.ResourceName = name
			app.UID = "legacy-uid"
			Expect(ensureNamespace(ctx, k8sClient, app)).To(Succeed())
			r := &LocoResourceReconciler{Client: k8sClient}
			Expect(r.ensureService(ctx, app)).To(Succeed())
			services := &corev1.ServiceList{}
			Expect(k8sClient.List(ctx, services, client.InNamespace(getNamespace(app)))).To(Succeed())
			Expect(services.Items).To(HaveLen(1))
			Expect(services.Items[0].Name).To(Equal(getName(app)))
		}
	})

})
