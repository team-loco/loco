package controller

import (
	"context"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

const (
	testNetThree    = "203.0.113.0/24"
	serviceCIDRv4   = "10.96.0.0/12"
	podCIDRA        = "10.244.0.0/24"
	podCIDRB        = "10.244.1.0/24"
	serviceCIDRv6   = "2001:db8:5::/112"
	globalPodCIDRv6 = "2600:1f18:abcd:1::/64"
	shortPodCIDRv6  = "2600::/64"
)

func testNode(name string, podCIDRs []string, internalIPs ...string) *corev1.Node {
	addresses := make([]corev1.NodeAddress, 0, len(internalIPs)+1)
	for _, ip := range internalIPs {
		addresses = append(addresses, corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: ip})
	}
	addresses = append(addresses, corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: "198.51.100.200"})
	return &corev1.Node{
		Name:   name,
		Spec:   corev1.NodeSpec{PodCIDRs: podCIDRs},
		Status: corev1.NodeStatus{Addresses: addresses},
	}
}

func TestEgressExclusionsIPv4Cluster(t *testing.T) {
	discovered := []string{podCIDRB, testNetThree, testNetThree, "198.51.100.7/32", serviceCIDRv4}
	exclusions := egressExclusions(discovered)

	want := []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16", "172.16.0.0/12",
		"192.168.0.0/16", "198.18.0.0/15", "198.51.100.7/32", testNetThree, "224.0.0.0/4", "240.0.0.0/4",
	}
	assertStrings(t, "ipv4", exclusions.ipv4, want)
	assertStrings(t, "ipv6", exclusions.ipv6, []string{"64:ff9b::/96", "fc00::/7", "fe80::/10", "ff00::/8"})
}

func TestEgressExclusionsDualStackAndGlobalIPv6PodRanges(t *testing.T) {
	discovered := []string{
		podCIDRA,
		"fd00:10:244::/64",
		globalPodCIDRv6,
		serviceCIDRv6,
		globalPodCIDRv6,
		"203.0.113.9/32",
		"::ffff:198.51.100.0/120",
	}
	exclusions := egressExclusions(discovered)

	assertStrings(t, "ipv6", exclusions.ipv6, []string{
		"64:ff9b::/96", serviceCIDRv6, globalPodCIDRv6, "fc00::/7", "fe80::/10", "ff00::/8",
	})
	if !containsString(exclusions.ipv4, "203.0.113.9/32") || !containsString(exclusions.ipv4, "198.51.100.0/24") {
		t.Errorf("ipv4 exclusions %v missing a discovered range", exclusions.ipv4)
	}
	for _, cidr := range exclusions.ipv4 {
		if cidr == podCIDRA {
			t.Errorf("range already covered by 10.0.0.0/8 was added: %v", exclusions.ipv4)
		}
	}
}

func TestEgressExclusionsAreStable(t *testing.T) {
	first := egressExclusions([]string{testNetThree, shortPodCIDRv6, "198.51.100.1/32"})
	second := egressExclusions([]string{"198.51.100.1/32", shortPodCIDRv6, testNetThree, testNetThree})
	assertStrings(t, "ipv4", second.ipv4, first.ipv4)
	assertStrings(t, "ipv6", second.ipv6, first.ipv6)
}

func TestClusterAddressRangesReadsNodesAndServiceCIDRs(t *testing.T) {
	scheme := workspaceTestScheme(t)
	legacy := testNode("legacy", nil, "203.0.113.4")
	legacy.Spec.PodCIDR = "100.96.3.0/24"
	dual := testNode("dual", []string{podCIDRB, "2600:1f18::/64"}, "203.0.113.5", "2600:1f18::5")
	serviceCIDR := &networkingv1.ServiceCIDR{
		Name: "kubernetes",
		Spec: networkingv1.ServiceCIDRSpec{CIDRs: []string{serviceCIDRv4, serviceCIDRv6}},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(legacy, dual, serviceCIDR).Build()
	ranges, err := clusterAddressRanges(context.Background(), kubeClient)
	if err != nil {
		t.Fatalf("clusterAddressRanges: %v", err)
	}
	for _, want := range []string{
		"100.96.3.0/24", "203.0.113.4/32", podCIDRB, "2600:1f18::/64",
		"203.0.113.5/32", "2600:1f18::5/128", serviceCIDRv4, serviceCIDRv6,
	} {
		if !containsString(ranges, want) {
			t.Errorf("ranges %v missing %s", ranges, want)
		}
	}
	if containsString(ranges, "198.51.100.200/32") {
		t.Errorf("external address included: %v", ranges)
	}
}

func TestClusterAddressRangesWithoutServiceCIDRAPI(t *testing.T) {
	scheme := workspaceTestScheme(t)
	node := testNode("only", []string{podCIDRA}, "203.0.113.6")
	listWithoutServiceCIDRs := func(
		ctx context.Context,
		c client.WithWatch,
		list client.ObjectList,
		opts ...client.ListOption,
	) error {
		if _, ok := list.(*networkingv1.ServiceCIDRList); ok {
			kind := schema.GroupKind{Group: "networking.k8s.io", Kind: "ServiceCIDR"}
			return &meta.NoKindMatchError{GroupKind: kind}
		}
		return c.List(ctx, list, opts...)
	}
	funcs := interceptor.Funcs{List: listWithoutServiceCIDRs}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node).WithInterceptorFuncs(funcs).Build()
	ranges, err := clusterAddressRanges(context.Background(), kubeClient)
	if err != nil {
		t.Fatalf("clusterAddressRanges without ServiceCIDR API: %v", err)
	}
	if !containsString(ranges, "203.0.113.6/32") {
		t.Errorf("ranges %v missing the node address", ranges)
	}
}

func TestNodeRangesChangedOnlyForAddressChanges(t *testing.T) {
	funcs := nodeRangesChanged()
	before := testNode("n", []string{podCIDRA}, "203.0.113.7")
	sameRanges := before.DeepCopy()
	sameRanges.Labels = map[string]string{"touched": "true"}
	newPodCIDR := before.DeepCopy()
	newPodCIDR.Spec.PodCIDRs = []string{podCIDRA, shortPodCIDRv6}

	if funcs.Update(updateEvent(before, sameRanges)) {
		t.Error("label change on a node triggered a reconcile")
	}
	if !funcs.Update(updateEvent(before, newPodCIDR)) {
		t.Error("pod CIDR change on a node did not trigger a reconcile")
	}
}

func assertStrings(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

func updateEvent(oldNode, newNode *corev1.Node) event.UpdateEvent {
	return event.UpdateEvent{ObjectOld: oldNode, ObjectNew: newNode}
}
