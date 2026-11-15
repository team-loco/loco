package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestGatewayIngressFollowsEachPodPort(t *testing.T) {
	r := &ApplicationReconciler{Gateway: testGateway, LocoNamespace: testLocoNamespace}
	for _, port := range []int32{8080, 9090} {
		app := testApplication()
		app.Spec.ServiceSpec.Deployment.Port = new(port)
		deployment, err := desiredDeployment(
			app, testWorkspaceNamespace(app), envSecretMount{}, testTelemetry(), testMinReadySeconds,
		)
		if err != nil {
			t.Fatal(err)
		}
		containerPort := deployment.Spec.Template.Spec.Containers[0].Ports[0]
		policyPort := r.gatewayIngressPolicy(app).Spec.Ingress[0].Ports[0].Port
		if want := intstr.FromString(*containerPort.Name); *policyPort != want {
			t.Errorf("application port %d: gateway policy port = %v, want %v", port, *policyPort, want)
		}
	}
}
