package domains

import (
	"maps"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	testNamespace    = "platform"
	testGateway      = "ingress"
	testCertificate  = "wildcard"
	testDomain       = "apps.test"
	testOtherDomain  = "other.test"
	testGeneration   = 3
	testHTTPSListen  = "https-apps-0"
	testHTTPListener = "http-apps-0"
)

type listenerState struct {
	accepted   metav1.ConditionStatus
	programmed metav1.ConditionStatus
	generation int64
}

var readyListener = listenerState{
	accepted:   metav1.ConditionTrue,
	programmed: metav1.ConditionTrue,
	generation: testGeneration,
}

func gatewayWith(listeners map[gatewayv1.SectionName]listenerState) *gatewayv1.Gateway {
	gateway := &gatewayv1.Gateway{Name: testGateway, Namespace: testNamespace, Generation: testGeneration}
	host := gatewayv1.Hostname("*." + testDomain)
	for _, name := range slices.Sorted(maps.Keys(listeners)) {
		gateway.Spec.Listeners = append(gateway.Spec.Listeners, gatewayv1.Listener{Name: name, Hostname: &host})
		state := listeners[name]
		gateway.Status.Listeners = append(gateway.Status.Listeners, gatewayv1.ListenerStatus{
			Name: name,
			Conditions: []metav1.Condition{
				{
					Type:               string(gatewayv1.ListenerConditionAccepted),
					Status:             state.accepted,
					ObservedGeneration: state.generation,
				},
				{
					Type:               string(gatewayv1.ListenerConditionProgrammed),
					Status:             state.programmed,
					ObservedGeneration: state.generation,
				},
			},
		})
	}
	return gateway
}

func certificate(ready metav1.ConditionStatus, names ...string) *unstructured.Unstructured {
	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(certificateKind)
	cert.SetNamespace(testNamespace)
	cert.SetName(testCertificate)
	cert.SetGeneration(testGeneration)
	dnsNames := make([]any, 0, len(names))
	for _, name := range names {
		dnsNames = append(dnsNames, name)
	}
	cert.Object["spec"] = map[string]any{"dnsNames": dnsNames}
	cert.Object["status"] = map[string]any{"conditions": []any{map[string]any{
		"type": "Ready", "status": string(ready), "observedGeneration": int64(testGeneration),
	}}}
	return cert
}

func reporter(t *testing.T, certificateName string, objects ...client.Object) *Reporter {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return New(reader, Config{
		Namespace:   testNamespace,
		Gateway:     testGateway,
		Certificate: certificateName,
		Domains:     []string{testDomain, testOtherDomain},
	})
}

func served(t *testing.T, r *Reporter) []string {
	t.Helper()
	domains, err := r.Served(t.Context())
	if err != nil {
		t.Fatalf("Served: %v", err)
	}
	return domains
}

func bothListeners(https, http listenerState) map[gatewayv1.SectionName]listenerState {
	return map[gatewayv1.SectionName]listenerState{testHTTPSListen: https, testHTTPListener: http}
}

func TestServedReportsADomainWhoseListenersAndCertificateAreReady(t *testing.T) {
	gateway := gatewayWith(bothListeners(readyListener, readyListener))
	cert := certificate(metav1.ConditionTrue, "*."+testDomain, "*."+testOtherDomain)
	if got := served(t, reporter(t, testCertificate, gateway, cert)); !slices.Equal(got, []string{testDomain}) {
		t.Fatalf("served = %v, want [%s] and not the domain with no listener", got, testDomain)
	}
}

func TestServedSkipsADomainWithAListenerThatIsNotReady(t *testing.T) {
	notAccepted := readyListener
	notAccepted.accepted = metav1.ConditionFalse
	notProgrammed := readyListener
	notProgrammed.programmed = metav1.ConditionFalse
	stale := readyListener
	stale.generation = testGeneration - 1
	for name, state := range map[string]listenerState{
		"not accepted":   notAccepted,
		"not programmed": notProgrammed,
		"stale":          stale,
	} {
		t.Run(name, func(t *testing.T) {
			gateway := gatewayWith(bothListeners(readyListener, state))
			cert := certificate(metav1.ConditionTrue, "*."+testDomain)
			if got := served(t, reporter(t, testCertificate, gateway, cert)); len(got) != 0 {
				t.Fatalf("served = %v, want none", got)
			}
		})
	}
}

func TestServedSkipsADomainWhoseCertificateIsNotReady(t *testing.T) {
	gateway := gatewayWith(bothListeners(readyListener, readyListener))
	cases := map[string]*Reporter{
		"not ready": reporter(t, testCertificate, gateway, certificate(metav1.ConditionFalse, "*."+testDomain)),
		"missing":   reporter(t, testCertificate, gateway),
		"name not included": reporter(
			t,
			testCertificate,
			gateway,
			certificate(metav1.ConditionTrue, "*."+testOtherDomain),
		),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if got := served(t, r); len(got) != 0 {
				t.Fatalf("served = %v, want none", got)
			}
		})
	}
}

func TestServedWithoutAManagedCertificateReliesOnTheListeners(t *testing.T) {
	gateway := gatewayWith(bothListeners(readyListener, readyListener))
	if got := served(t, reporter(t, "", gateway)); !slices.Equal(got, []string{testDomain}) {
		t.Fatalf("served = %v, want [%s]", got, testDomain)
	}
}

func TestServedIsEmptyWithoutAGateway(t *testing.T) {
	if got := served(t, reporter(t, testCertificate)); len(got) != 0 {
		t.Fatalf("served = %v, want none", got)
	}
}
