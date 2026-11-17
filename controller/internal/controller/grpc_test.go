package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestPublicServiceSupportsHTTPAndGRPC(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1Gateway.Install(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &ApplicationReconciler{Client: kube, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix}
	app := testApplication()
	if err := r.ensureService(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	var service corev1.Service
	key := client.ObjectKey{
		Namespace: testWorkspaceNamespace(app),
		Name:      locov1alpha1.ApplicationName(app.Spec.ResourceID),
	}
	if err := kube.Get(t.Context(), key, &service); err != nil {
		t.Fatal(err)
	}
	if len(service.Spec.Ports) != 2 {
		t.Fatalf("ports = %+v", service.Spec.Ports)
	}
	http, grpc := service.Spec.Ports[0], service.Spec.Ports[1]
	if http.Port != servicePort || http.AppProtocol != nil || grpc.Port != grpcServicePort ||
		grpc.AppProtocol == nil || *grpc.AppProtocol != "kubernetes.io/h2c" || grpc.TargetPort != http.TargetPort {
		t.Fatalf("HTTP and gRPC ports = %+v", service.Spec.Ports)
	}
	if err := r.ensureHTTPRoute(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	var route v1Gateway.HTTPRoute
	key.Name = getRouteName(app)
	if err := kube.Get(t.Context(), key, &route); err != nil {
		t.Fatal(err)
	}
	if len(route.Spec.Rules) != 2 {
		t.Fatalf("rules = %+v", route.Spec.Rules)
	}
	grpcRule, httpRule := route.Spec.Rules[0], route.Spec.Rules[1]
	if *grpcRule.BackendRefs[0].Port != grpcServicePort || *httpRule.BackendRefs[0].Port != servicePort ||
		grpcRule.BackendRefs[0].Name != httpRule.BackendRefs[0].Name ||
		len(grpcRule.Matches[0].Headers) != 1 || grpcRule.Matches[0].Headers[0].Name != "content-type" ||
		len(httpRule.Matches[0].Headers) != 0 {
		t.Fatalf("rules = %+v", route.Spec.Rules)
	}
	if *grpcRule.Matches[0].Path.Value != app.Spec.ServiceSpec.Routing.PathPrefix {
		t.Fatal("gRPC rule does not preserve the configured path prefix")
	}
	app.Spec.ServiceSpec.Routing = nil
	if err := r.ensureService(t.Context(), app); err != nil {
		t.Fatal(err)
	}
	key.Name = locov1alpha1.ApplicationName(app.Spec.ResourceID)
	if err := kube.Get(t.Context(), key, &service); err != nil {
		t.Fatal(err)
	}
	if len(service.Spec.Ports) != 1 || service.Spec.Ports[0].Port != servicePort {
		t.Fatalf("private service ports = %+v", service.Spec.Ports)
	}
}
