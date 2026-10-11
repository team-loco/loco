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

	"github.com/team-loco/loco/controller/internal/isolation"
	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	labelLocoApp           = "loco.io/app"
	labelNamespaceName     = "kubernetes.io/metadata.name"
	labelGatewayName       = "gateway.envoyproxy.io/owning-gateway-name"
	labelGatewayNamespace  = "gateway.envoyproxy.io/owning-gateway-namespace"
	labelAppKubernetesName = "app.kubernetes.io/name"
	gatewayName            = "eg"
	podSecurityLevel       = "restricted"
	podSecurityVersion     = "latest"
	policyWorkspaceAccess  = "allow-workspace"
	policyTelemetryEgress  = "allow-telemetry-egress"
)

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
	labels[managed.LabelManagedBy] = managed.ManagedByValue
	labels[managed.LabelWorkspaceID] = locoRes.Spec.WorkspaceID
	labels[labelEnvironmentID] = locoRes.Spec.EnvironmentID
	return labels
}

func workspaceObjectLabels(locoRes *locov1alpha1.Application) map[string]string {
	return map[string]string{
		managed.LabelManagedBy:   managed.ManagedByValue,
		managed.LabelWorkspaceID: locoRes.Spec.WorkspaceID,
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

func tcpPort(port int32) *networkingv1ac.NetworkPolicyPortApplyConfiguration {
	value := intstr.FromInt32(port)
	return networkingv1ac.NetworkPolicyPort().
		WithProtocol(corev1.ProtocolTCP).
		WithPort(value)
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

func (r *LocoResourceReconciler) workspaceNetworkPolicies(
	locoRes *locov1alpha1.Application,
	exclusions isolation.EgressExclusions,
) []*networkingv1ac.NetworkPolicyApplyConfiguration {
	namespace := getNamespace(locoRes)
	labels := workspaceObjectLabels(locoRes)
	ingress := networkingv1.PolicyTypeIngress
	egress := networkingv1.PolicyTypeEgress

	denyAll := isolation.DenyAllSpec()

	workspaceIngressPeer := sameNamespacePeer()
	workspaceEgressPeer := sameNamespacePeer()
	workspaceIngress := networkingv1ac.NetworkPolicyIngressRule().WithFrom(workspaceIngressPeer)
	workspaceEgress := networkingv1ac.NetworkPolicyEgressRule().WithTo(workspaceEgressPeer)
	workspaceAccess := networkingv1ac.NetworkPolicySpec().
		WithPolicyTypes(ingress, egress).
		WithIngress(workspaceIngress).
		WithEgress(workspaceEgress)

	dnsEgress := isolation.DNSEgressSpec()

	telemetryLabels := map[string]string{labelAppKubernetesName: r.Telemetry.CollectorService}
	telemetryPeer := namespacedPodPeer(r.Telemetry.Namespace, telemetryLabels)
	grpcPort := tcpPort(r.Telemetry.GRPCPort)
	httpPort := tcpPort(r.Telemetry.HTTPPort)
	telemetryRule := networkingv1ac.NetworkPolicyEgressRule().
		WithTo(telemetryPeer).
		WithPorts(grpcPort, httpPort)
	telemetryEgress := networkingv1ac.NetworkPolicySpec().WithPolicyTypes(egress).WithEgress(telemetryRule)

	internetEgress := isolation.InternetEgressSpec(exclusions)

	return []*networkingv1ac.NetworkPolicyApplyConfiguration{
		isolation.NamespacePolicy(isolation.PolicyDefaultDeny, namespace, labels, denyAll),
		isolation.NamespacePolicy(policyWorkspaceAccess, namespace, labels, workspaceAccess),
		isolation.NamespacePolicy(isolation.PolicyDNSEgress, namespace, labels, dnsEgress),
		isolation.NamespacePolicy(policyTelemetryEgress, namespace, labels, telemetryEgress),
		isolation.NamespacePolicy(isolation.PolicyInternetEgress, namespace, labels, internetEgress),
	}
}

func (r *LocoResourceReconciler) ensureWorkspaceNetworkPolicies(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
) error {
	exclusions, err := isolation.DiscoverEgressExclusions(ctx, r.Client)
	if err != nil {
		return err
	}
	policies := r.workspaceNetworkPolicies(locoRes, exclusions)
	return isolation.ApplyPolicies(ctx, r.Client, policies)
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
		labelGatewayNamespace: r.LocoNamespace,
	}
	gatewayPeer := namespacedPodPeer(r.LocoNamespace, gatewayLabels)
	containerPort := locoRes.Spec.ServiceSpec.Deployment.Port
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
	opts := managed.ApplyOptions()
	if err := r.Apply(ctx, policy, opts...); err != nil {
		return fmt.Errorf("apply network policy %s/%s: %w", namespace, policyName, err)
	}
	return nil
}
