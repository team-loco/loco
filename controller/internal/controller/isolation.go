package controller

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	labelLocoApp            = "loco.io/app"
	labelNamespaceName      = "kubernetes.io/metadata.name"
	labelGatewayName        = "gateway.envoyproxy.io/owning-gateway-name"
	labelGatewayNamespace   = "gateway.envoyproxy.io/owning-gateway-namespace"
	labelAppKubernetesName  = "app.kubernetes.io/name"
	labelK8sApp             = "k8s-app"
	gatewayName             = "eg"
	defaultObsNamespace     = "observability"
	otelCollectorName       = "otel-col-deploy"
	dnsNamespace            = "kube-system"
	dnsAppLabel             = "kube-dns"
	podSecurityLevel        = "restricted"
	podSecurityVersion      = "latest"
	policyDefaultDeny       = "default-deny"
	policyGatewayIngress    = "allow-gateway-ingress"
	policyEnvironmentAccess = "allow-environment"
	policyDNSEgress         = "allow-dns-egress"
	policyInternetEgress    = "allow-internet-egress"
	policyTelemetryEgress   = "allow-telemetry-egress"
)

var nonPublicIPv4 = []string{
	"10.0.0.0/8",
	"100.64.0.0/10",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.168.0.0/16",
}

var nonPublicIPv6 = []string{
	"fc00::/7",
	"fe80::/10",
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

func namespaceLabels(locoRes *locov1alpha1.Application) map[string]string {
	labels := podSecurityLabels()
	labels[labelLocoApp] = "true"
	labels[labelWorkspaceID] = locoRes.Spec.WorkspaceID
	labels[labelResourceID] = locoRes.Spec.ResourceID
	labels[labelEnvironmentID] = locoRes.Spec.EnvironmentID
	return labels
}

func appPort(locoRes *locov1alpha1.Application) int32 {
	if locoRes.Spec.ServiceSpec.Deployment.Port > 0 {
		return locoRes.Spec.ServiceSpec.Deployment.Port
	}
	return 8080
}

func podSecurityContext() *corev1.PodSecurityContext {
	runAsNonRoot := true
	return &corev1.PodSecurityContext{
		RunAsNonRoot: &runAsNonRoot,
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

func containerSecurityContext() *corev1.SecurityContext {
	allowPrivilegeEscalation := false
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: &allowPrivilegeEscalation,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
}

func publicIPBlocks() []networkingv1.NetworkPolicyPeer {
	return []networkingv1.NetworkPolicyPeer{
		{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: nonPublicIPv4}},
		{IPBlock: &networkingv1.IPBlock{CIDR: "::/0", Except: nonPublicIPv6}},
	}
}

func tcpPorts(ports ...int) []networkingv1.NetworkPolicyPort {
	tcp := corev1.ProtocolTCP
	out := make([]networkingv1.NetworkPolicyPort, 0, len(ports))
	for _, port := range ports {
		portValue := intstr.FromInt(port)
		out = append(out, networkingv1.NetworkPolicyPort{Protocol: &tcp, Port: &portValue})
	}
	return out
}

func dnsPorts() []networkingv1.NetworkPolicyPort {
	udp := corev1.ProtocolUDP
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt(53)
	return []networkingv1.NetworkPolicyPort{
		{Protocol: &udp, Port: &port},
		{Protocol: &tcp, Port: &port},
	}
}

func environmentPeer(locoRes *locov1alpha1.Application) networkingv1.NetworkPolicyPeer {
	return networkingv1.NetworkPolicyPeer{
		NamespaceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				labelLocoApp:       "true",
				labelWorkspaceID:   locoRes.Spec.WorkspaceID,
				labelEnvironmentID: locoRes.Spec.EnvironmentID,
			},
		},
	}
}

func tenantNetworkPolicies(
	locoRes *locov1alpha1.Application,
	locoNamespace string,
	obsNamespace string,
) []networkingv1.NetworkPolicy {
	namespace := getNamespace(locoRes)
	allPods := metav1.LabelSelector{}
	ingress := []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	egress := []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}
	port := int(appPort(locoRes))

	policy := func(name string, spec networkingv1.NetworkPolicySpec) networkingv1.NetworkPolicy {
		spec.PodSelector = allPods
		return networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    map[string]string{labelApp: getName(locoRes)},
			},
			Spec: spec,
		}
	}

	return []networkingv1.NetworkPolicy{
		policy(policyDefaultDeny, networkingv1.NetworkPolicySpec{
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		}),
		policy(policyGatewayIngress, networkingv1.NetworkPolicySpec{
			PolicyTypes: ingress,
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{labelNamespaceName: locoNamespace},
					},
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							labelGatewayName:      gatewayName,
							labelGatewayNamespace: locoNamespace,
						},
					},
				}},
				Ports: tcpPorts(port),
			}},
		}),
		policy(policyEnvironmentAccess, networkingv1.NetworkPolicySpec{
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{environmentPeer(locoRes)},
				Ports: tcpPorts(port),
			}},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To: []networkingv1.NetworkPolicyPeer{environmentPeer(locoRes)},
			}},
		}),
		policy(policyDNSEgress, networkingv1.NetworkPolicySpec{
			PolicyTypes: egress,
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{labelNamespaceName: dnsNamespace},
					},
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{labelK8sApp: dnsAppLabel},
					},
				}},
				Ports: dnsPorts(),
			}},
		}),
		policy(policyTelemetryEgress, networkingv1.NetworkPolicySpec{
			PolicyTypes: egress,
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{labelNamespaceName: obsNamespace},
					},
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{labelAppKubernetesName: otelCollectorName},
					},
				}},
				Ports: tcpPorts(4317, 4318),
			}},
		}),
		policy(policyInternetEgress, networkingv1.NetworkPolicySpec{
			PolicyTypes: egress,
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To: publicIPBlocks(),
			}},
		}),
	}
}

func (r *LocoResourceReconciler) ensureNetworkPolicies(ctx context.Context, locoRes *locov1alpha1.Application) error {
	for _, desired := range tenantNetworkPolicies(locoRes, r.locoNamespace, r.obsNamespace) {
		current := &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
		}
		op, err := controllerutil.CreateOrUpdate(ctx, r.Client, current, func() error {
			current.Labels = desired.Labels
			current.Spec = desired.Spec
			return nil
		})
		if err != nil {
			return fmt.Errorf("ensure network policy %s/%s: %w", desired.Namespace, desired.Name, err)
		}
		slog.InfoContext(ctx, "network policy ensured", "namespace", desired.Namespace, "name", desired.Name, "op", op)
	}
	return nil
}
