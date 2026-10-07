package isolation

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/controller/internal/managed"
)

const (
	PolicyDefaultDeny    = "default-deny"
	PolicyDNSEgress      = "allow-dns-egress"
	PolicyInternetEgress = "allow-internet-egress"
	dnsPort              = int32(53)
	anyIPv4              = "0.0.0.0/0"
	anyIPv6              = "::/0"
)

var reservedIPv4 = []string{
	"0.0.0.0/8",      // rfc 1122 "this network"
	"10.0.0.0/8",     // rfc 1918 private network
	"100.64.0.0/10",  // rfc 6598 carrier-grade nat
	"169.254.0.0/16", // rfc 3927 link-local, includes the cloud metadata endpoint 169.254.169.254
	"172.16.0.0/12",  // rfc 1918 private network
	"192.168.0.0/16", // rfc 1918 private network
	"198.18.0.0/15",  // rfc 2544 benchmarking
	"224.0.0.0/4",    // multicast
	"240.0.0.0/4",    // reserved for future use
}

var reservedIPv6 = []string{
	"64:ff9b::/96", // rfc 6052 nat64, can translate to internal ipv4
	"fc00::/7",     // rfc 4193 unique local addresses
	"fe80::/10",    // rfc 4291 link-local
	"ff00::/8",     // multicast
}

func dnsPorts() []*networkingv1ac.NetworkPolicyPortApplyConfiguration {
	value := intstr.FromInt32(dnsPort)
	udp := networkingv1ac.NetworkPolicyPort().WithProtocol(corev1.ProtocolUDP).WithPort(value)
	tcp := networkingv1ac.NetworkPolicyPort().WithProtocol(corev1.ProtocolTCP).WithPort(value)
	return []*networkingv1ac.NetworkPolicyPortApplyConfiguration{udp, tcp}
}

func publicAddressPeers(exclusions EgressExclusions) []*networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	ipv4 := networkingv1ac.IPBlock().WithCIDR(anyIPv4).WithExcept(exclusions.ipv4...)
	ipv6 := networkingv1ac.IPBlock().WithCIDR(anyIPv6).WithExcept(exclusions.ipv6...)
	ipv4Peer := networkingv1ac.NetworkPolicyPeer().WithIPBlock(ipv4)
	ipv6Peer := networkingv1ac.NetworkPolicyPeer().WithIPBlock(ipv6)
	return []*networkingv1ac.NetworkPolicyPeerApplyConfiguration{ipv4Peer, ipv6Peer}
}

func DenyAllSpec() *networkingv1ac.NetworkPolicySpecApplyConfiguration {
	return networkingv1ac.NetworkPolicySpec().
		WithPolicyTypes(networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress)
}

func DNSEgressSpec() *networkingv1ac.NetworkPolicySpecApplyConfiguration {
	dnsPortList := dnsPorts()
	dnsRule := networkingv1ac.NetworkPolicyEgressRule().WithPorts(dnsPortList...)
	return networkingv1ac.NetworkPolicySpec().
		WithPolicyTypes(networkingv1.PolicyTypeEgress).
		WithEgress(dnsRule)
}

func InternetEgressSpec(exclusions EgressExclusions) *networkingv1ac.NetworkPolicySpecApplyConfiguration {
	publicPeers := publicAddressPeers(exclusions)
	internetRule := networkingv1ac.NetworkPolicyEgressRule().WithTo(publicPeers...)
	return networkingv1ac.NetworkPolicySpec().
		WithPolicyTypes(networkingv1.PolicyTypeEgress).
		WithEgress(internetRule)
}

func NamespacePolicy(
	name string,
	namespace string,
	labels map[string]string,
	spec *networkingv1ac.NetworkPolicySpecApplyConfiguration,
) *networkingv1ac.NetworkPolicyApplyConfiguration {
	allPods := metav1ac.LabelSelector()
	spec.WithPodSelector(allPods)
	return networkingv1ac.NetworkPolicy(name, namespace).
		WithLabels(labels).
		WithSpec(spec)
}

func ApplyPolicies(
	ctx context.Context,
	kubeClient client.Client,
	policies []*networkingv1ac.NetworkPolicyApplyConfiguration,
) error {
	opts := managed.ApplyOptions()
	for _, policy := range policies {
		if err := kubeClient.Apply(ctx, policy, opts...); err != nil {
			return fmt.Errorf("apply network policy %s/%s: %w", *policy.Namespace, *policy.Name, err)
		}
	}
	return nil
}
