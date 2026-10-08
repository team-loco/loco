package controller

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	labelLocoApp           = "loco.io/app"
	labelNamespaceName     = "kubernetes.io/metadata.name"
	labelGatewayName       = "gateway.envoyproxy.io/owning-gateway-name"
	labelGatewayNamespace  = "gateway.envoyproxy.io/owning-gateway-namespace"
	labelAppKubernetesName = "app.kubernetes.io/name"
	gatewayName            = "eg"
	defaultObsNamespace    = "observability"
	otelCollectorName      = "otel-col-deploy"
	podSecurityLevel       = "restricted"
	podSecurityVersion     = "latest"
	policyDefaultDeny      = "default-deny"
	policyWorkspaceAccess  = "allow-workspace"
	policyDNSEgress        = "allow-dns-egress"
	policyTelemetryEgress  = "allow-telemetry-egress"
	policyInternetEgress   = "allow-internet-egress"
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

func podSecurityLabels() map[string]string {
	return map[string]string{
		"pod-security.kubernetes.io/enforce":         podSecurityLevel,
		"pod-security.kubernetes.io/enforce-version": podSecurityVersion,
		"pod-security.kubernetes.io/audit":           podSecurityLevel,
		"pod-security.kubernetes.io/audit-version":   podSecurityVersion,
		"pod-security.kubernetes.io/warn":            podSecurityLevel,
		"pod-security.kubernetes.io/warn-version":    podSecurityVersion,
	}
}

func workspaceNamespaceLabels(locoRes *locov1alpha1.Application) map[string]string {
	labels := podSecurityLabels()
	labels[labelLocoApp] = "true"
	labels[labelManagedBy] = managedByValue
	labels[labelWorkspaceID] = locoRes.Spec.WorkspaceID
	labels[labelEnvironmentID] = locoRes.Spec.EnvironmentID
	return labels
}

func workspaceObjectLabels(locoRes *locov1alpha1.Application) map[string]string {
	return map[string]string{
		labelManagedBy:   managedByValue,
		labelWorkspaceID: locoRes.Spec.WorkspaceID,
	}
}

func podSecurityContext() *corev1ac.PodSecurityContextApplyConfiguration {
	seccomp := corev1ac.SeccompProfile().WithType(corev1.SeccompProfileTypeRuntimeDefault)
	return corev1ac.PodSecurityContext().
		WithRunAsNonRoot(true).
		WithSeccompProfile(seccomp)
}

func containerSecurityContext() *corev1ac.SecurityContextApplyConfiguration {
	capabilities := corev1ac.Capabilities().WithDrop("ALL")
	return corev1ac.SecurityContext().
		WithAllowPrivilegeEscalation(false).
		WithCapabilities(capabilities)
}

func (r *LocoResourceReconciler) telemetryNamespace() string {
	if r.obsNamespace != "" {
		return r.obsNamespace
	}
	return defaultObsNamespace
}

func tcpPort(port int32) *networkingv1ac.NetworkPolicyPortApplyConfiguration {
	value := intstr.FromInt32(port)
	return networkingv1ac.NetworkPolicyPort().
		WithProtocol(corev1.ProtocolTCP).
		WithPort(value)
}

func dnsPorts() []*networkingv1ac.NetworkPolicyPortApplyConfiguration {
	value := intstr.FromInt32(53)
	udp := networkingv1ac.NetworkPolicyPort().WithProtocol(corev1.ProtocolUDP).WithPort(value)
	tcp := networkingv1ac.NetworkPolicyPort().WithProtocol(corev1.ProtocolTCP).WithPort(value)
	return []*networkingv1ac.NetworkPolicyPortApplyConfiguration{udp, tcp}
}

func namespacedPodPeer(
	namespace string,
	podLabels map[string]string,
) *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	namespaceLabels := map[string]string{labelNamespaceName: namespace}
	namespaceSelector := metav1ac.LabelSelector().WithMatchLabels(namespaceLabels)
	podSelector := metav1ac.LabelSelector().WithMatchLabels(podLabels)
	return networkingv1ac.NetworkPolicyPeer().
		WithNamespaceSelector(namespaceSelector).
		WithPodSelector(podSelector)
}

func sameNamespacePeer() *networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	allPods := metav1ac.LabelSelector()
	return networkingv1ac.NetworkPolicyPeer().WithPodSelector(allPods)
}

func publicAddressPeers(
	exclusions egressExclusionSet,
) []*networkingv1ac.NetworkPolicyPeerApplyConfiguration {
	ipv4 := networkingv1ac.IPBlock().WithCIDR("0.0.0.0/0").WithExcept(exclusions.ipv4...)
	ipv6 := networkingv1ac.IPBlock().WithCIDR("::/0").WithExcept(exclusions.ipv6...)
	ipv4Peer := networkingv1ac.NetworkPolicyPeer().WithIPBlock(ipv4)
	ipv6Peer := networkingv1ac.NetworkPolicyPeer().WithIPBlock(ipv6)
	return []*networkingv1ac.NetworkPolicyPeerApplyConfiguration{ipv4Peer, ipv6Peer}
}

func denyAllSpec() *networkingv1ac.NetworkPolicySpecApplyConfiguration {
	return networkingv1ac.NetworkPolicySpec().
		WithPolicyTypes(networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress)
}

func dnsEgressSpec() *networkingv1ac.NetworkPolicySpecApplyConfiguration {
	dnsPortList := dnsPorts()
	dnsRule := networkingv1ac.NetworkPolicyEgressRule().WithPorts(dnsPortList...)
	return networkingv1ac.NetworkPolicySpec().
		WithPolicyTypes(networkingv1.PolicyTypeEgress).
		WithEgress(dnsRule)
}

func internetEgressSpec(exclusions egressExclusionSet) *networkingv1ac.NetworkPolicySpecApplyConfiguration {
	publicPeers := publicAddressPeers(exclusions)
	internetRule := networkingv1ac.NetworkPolicyEgressRule().WithTo(publicPeers...)
	return networkingv1ac.NetworkPolicySpec().
		WithPolicyTypes(networkingv1.PolicyTypeEgress).
		WithEgress(internetRule)
}

func (r *LocoResourceReconciler) workspaceNetworkPolicies(
	locoRes *locov1alpha1.Application,
	exclusions egressExclusionSet,
) []*networkingv1ac.NetworkPolicyApplyConfiguration {
	namespace := getNamespace(locoRes)
	labels := workspaceObjectLabels(locoRes)
	ingress := networkingv1.PolicyTypeIngress
	egress := networkingv1.PolicyTypeEgress

	denyAll := denyAllSpec()

	workspaceIngressPeer := sameNamespacePeer()
	workspaceEgressPeer := sameNamespacePeer()
	workspaceIngress := networkingv1ac.NetworkPolicyIngressRule().WithFrom(workspaceIngressPeer)
	workspaceEgress := networkingv1ac.NetworkPolicyEgressRule().WithTo(workspaceEgressPeer)
	workspaceAccess := networkingv1ac.NetworkPolicySpec().
		WithPolicyTypes(ingress, egress).
		WithIngress(workspaceIngress).
		WithEgress(workspaceEgress)

	dnsEgress := dnsEgressSpec()

	telemetryLabels := map[string]string{labelAppKubernetesName: otelCollectorName}
	obsNamespace := r.telemetryNamespace()
	telemetryPeer := namespacedPodPeer(obsNamespace, telemetryLabels)
	grpcPort := tcpPort(4317)
	httpPort := tcpPort(4318)
	telemetryRule := networkingv1ac.NetworkPolicyEgressRule().
		WithTo(telemetryPeer).
		WithPorts(grpcPort, httpPort)
	telemetryEgress := networkingv1ac.NetworkPolicySpec().WithPolicyTypes(egress).WithEgress(telemetryRule)

	internetEgress := internetEgressSpec(exclusions)

	return []*networkingv1ac.NetworkPolicyApplyConfiguration{
		namespacePolicy(policyDefaultDeny, namespace, labels, denyAll),
		namespacePolicy(policyWorkspaceAccess, namespace, labels, workspaceAccess),
		namespacePolicy(policyDNSEgress, namespace, labels, dnsEgress),
		namespacePolicy(policyTelemetryEgress, namespace, labels, telemetryEgress),
		namespacePolicy(policyInternetEgress, namespace, labels, internetEgress),
	}
}

func namespacePolicy(
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

func applyPolicies(
	ctx context.Context,
	kubeClient client.Client,
	policies []*networkingv1ac.NetworkPolicyApplyConfiguration,
) error {
	opts := applyOptions()
	for _, policy := range policies {
		if err := kubeClient.Apply(ctx, policy, opts...); err != nil {
			return fmt.Errorf("apply network policy %s/%s: %w", *policy.Namespace, *policy.Name, err)
		}
	}
	return nil
}

func (r *LocoResourceReconciler) ensureWorkspaceNetworkPolicies(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
) error {
	exclusions, err := discoverEgressExclusions(ctx, r.Client)
	if err != nil {
		return err
	}
	policies := r.workspaceNetworkPolicies(locoRes, exclusions)
	return applyPolicies(ctx, r.Client, policies)
}

func (r *LocoResourceReconciler) gatewayIngressPolicy(
	locoRes *locov1alpha1.Application,
) *networkingv1ac.NetworkPolicyApplyConfiguration {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	policyName := getGatewayPolicyName(locoRes)
	labels := managedLabels(locoRes)
	annotations := ownerAnnotations(locoRes)

	appLabels := map[string]string{labelApp: name}
	appSelector := metav1ac.LabelSelector().WithMatchLabels(appLabels)
	gatewayLabels := map[string]string{
		labelGatewayName:      gatewayName,
		labelGatewayNamespace: r.locoNamespace,
	}
	gatewayPeer := namespacedPodPeer(r.locoNamespace, gatewayLabels)
	containerPort := appPort(locoRes)
	port := tcpPort(containerPort)
	rule := networkingv1ac.NetworkPolicyIngressRule().WithFrom(gatewayPeer).WithPorts(port)
	spec := networkingv1ac.NetworkPolicySpec().
		WithPodSelector(appSelector).
		WithPolicyTypes(networkingv1.PolicyTypeIngress).
		WithIngress(rule)

	return networkingv1ac.NetworkPolicy(policyName, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithSpec(spec)
}

func (r *LocoResourceReconciler) ensureGatewayIngressPolicy(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
) error {
	namespace := getNamespace(locoRes)
	policyName := getGatewayPolicyName(locoRes)

	if locoRes.Spec.ServiceSpec.Routing == nil {
		existing := &networkingv1.NetworkPolicy{Name: policyName, Namespace: namespace}
		if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete network policy %s/%s: %w", namespace, policyName, err)
		}
		return nil
	}

	slog.DebugContext(ctx, "ensuring gateway ingress policy", "namespace", namespace, "name", policyName)
	policy := r.gatewayIngressPolicy(locoRes)
	opts := applyOptions()
	if err := r.Apply(ctx, policy, opts...); err != nil {
		return fmt.Errorf("apply network policy %s/%s: %w", namespace, policyName, err)
	}
	return nil
}
