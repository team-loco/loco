package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	EnvWorkspaceDNS         = "LOCO_WORKSPACE_DNS"
	workspaceDNSName        = "resource-00000000-0000-0000-0000-000000000000"
	componentWorkspaceDNS   = "workspace-dns"
	workspaceDNSPort        = int32(1053)
	workspaceDNSServicePort = int32(53)
	workspaceDNSHealthPort  = int32(8080)
	workspaceDNSReadyPort   = int32(8181)
	workspaceDNSUser        = int64(65532)
	workspaceDNSNdots       = "5"
)

var (
	workspaceDNSImagePattern = regexp.MustCompile(`^[^[:space:]@]+:[^[:space:]@/]+@sha256:[a-f0-9]{64}$`)
	errWorkspaceDNSConfig    = errors.New(
		"workspace DNS requires a digest-pinned image, a cluster domain, positive replicas and resource requests and limits",
	)
	errWorkspaceDNSCollision = errors.New("workspace DNS object belongs to another owner")
	errWorkspaceDNSAddress   = errors.New("workspace DNS requires an allocated ClusterIP")
)

type WorkspaceDNSConfig struct {
	Image         string                      `json:"image"`
	ClusterDomain string                      `json:"clusterDomain"`
	Replicas      int32                       `json:"replicas"`
	Resources     corev1.ResourceRequirements `json:"resources"`
}

func (c WorkspaceDNSConfig) valid() bool {
	if !workspaceDNSImagePattern.MatchString(c.Image) || len(validation.IsDNS1123Subdomain(c.ClusterDomain)) != 0 ||
		c.ClusterDomain == "" ||
		c.Replicas <= 0 {
		return false
	}
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		request, hasRequest := c.Resources.Requests[name]
		limit, hasLimit := c.Resources.Limits[name]
		if !hasRequest || !hasLimit || request.Sign() <= 0 || limit.Cmp(request) < 0 {
			return false
		}
	}
	return true
}

func ParseWorkspaceDNSConfig(raw string) (WorkspaceDNSConfig, error) {
	var config WorkspaceDNSConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return config, fmt.Errorf("decode workspace DNS configuration: %w", err)
	}
	if !config.valid() {
		return config, errWorkspaceDNSConfig
	}
	return config, nil
}

func (c WorkspaceDNSConfig) corefile(namespace string) string {
	canonical := "{1}." + namespace + ".svc." + c.ClusterDomain + "."
	suffixes := []string{
		".",
		"." + namespace + ".svc." + c.ClusterDomain + ".",
		".svc." + c.ClusterDomain + ".",
		"." + c.ClusterDomain + ".",
	}
	var rules strings.Builder
	for _, suffix := range suffixes {
		pattern := "^([a-z0-9](?:[-a-z0-9]*[a-z0-9])?)\\.local" + regexp.QuoteMeta(suffix) + "$"
		fmt.Fprintf(
			&rules,
			"    rewrite stop {\n        name regex %s %s\n        answer auto\n    }\n",
			pattern,
			canonical,
		)
	}
	return fmt.Sprintf(
		".:%d {\n    errors\n    health :%d\n    ready :%d\n%s    forward . /etc/resolv.conf\n    loop\n    reload\n}\n",
		workspaceDNSPort,
		workspaceDNSHealthPort,
		workspaceDNSReadyPort,
		rules.String(),
	)
}

func (r *LocoResourceReconciler) ensureWorkspaceDNS(
	ctx context.Context,
	app *locov1alpha1.Application,
) (string, bool, error) {
	if r.WorkspaceDNS.Image == "" {
		return "", true, nil
	}
	namespace := &corev1.Namespace{}
	key := client.ObjectKey{Name: getNamespace(app)}
	if err := r.apiReader().Get(ctx, key, namespace); err != nil {
		return "", false, fmt.Errorf("read DNS namespace: %w", err)
	}
	labels := map[string]string{
		managed.LabelManagedBy:   managed.ManagedByValue,
		managed.LabelComponent:   componentWorkspaceDNS,
		managed.LabelWorkspaceID: app.Spec.WorkspaceID,
	}
	owner := metav1.OwnerReference{
		APIVersion: "v1",
		Kind:       "Namespace",
		Name:       namespace.Name,
		UID:        namespace.UID,
		Controller: new(true),
	}
	metadata := metav1.ObjectMeta{
		Name:            workspaceDNSName,
		Namespace:       namespace.Name,
		Labels:          labels,
		OwnerReferences: []metav1.OwnerReference{owner},
	}
	config := &corev1.ConfigMap{
		APIVersion: "v1", Kind: "ConfigMap",
		ObjectMeta: metadata,
		Data:       map[string]string{"Corefile": r.WorkspaceDNS.corefile(namespace.Name)},
	}
	target := intstr.FromInt32(workspaceDNSPort)
	service := &corev1.Service{
		APIVersion: "v1", Kind: "Service",
		ObjectMeta: metadata,
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: labels, Ports: []corev1.ServicePort{
			{Name: "dns-udp", Protocol: corev1.ProtocolUDP, Port: workspaceDNSServicePort, TargetPort: target},
			{Name: "dns-tcp", Protocol: corev1.ProtocolTCP, Port: workspaceDNSServicePort, TargetPort: target},
		}},
	}
	deployment := r.workspaceDNSDeployment(metadata, labels)
	for _, object := range []client.Object{config, service, deployment} {
		if err := r.applyWorkspaceDNSObject(ctx, namespace, object); err != nil {
			return "", false, err
		}
	}
	key.Namespace, key.Name = namespace.Name, workspaceDNSName
	if err := r.apiReader().Get(ctx, key, service); err != nil {
		return "", false, fmt.Errorf("read DNS service: %w", err)
	}
	if service.Spec.Type != corev1.ServiceTypeClusterIP || service.Spec.ClusterIP == "" ||
		service.Spec.ClusterIP == corev1.ClusterIPNone {
		return "", false, errWorkspaceDNSAddress
	}
	if err := r.apiReader().Get(ctx, key, deployment); err != nil {
		return "", false, fmt.Errorf("read DNS deployment: %w", err)
	}
	ready := deployment.Status.ObservedGeneration == deployment.Generation &&
		deployment.Status.UpdatedReplicas == r.WorkspaceDNS.Replicas &&
		deployment.Status.AvailableReplicas == r.WorkspaceDNS.Replicas
	return service.Spec.ClusterIP, ready, nil
}

func (r *LocoResourceReconciler) workspaceDNSDeployment(
	metadata metav1.ObjectMeta,
	labels map[string]string,
) *appsv1.Deployment {
	probe := func(path string, port int32) *corev1.Probe {
		return &corev1.Probe{
			HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(port)},
		}
	}
	return &appsv1.Deployment{
		APIVersion: "apps/v1", Kind: "Deployment",
		ObjectMeta: metadata,
		Spec: appsv1.DeploymentSpec{
			Replicas: new(r.WorkspaceDNS.Replicas), Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{
				AutomountServiceAccountToken: new(false),
				DNSPolicy:                    corev1.DNSClusterFirst,
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot:   new(true),
					RunAsUser:      new(workspaceDNSUser),
					SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				},
				Containers: []corev1.Container{
					{
						Name:      "dns",
						Image:     r.WorkspaceDNS.Image,
						Args:      []string{"-conf", "/etc/coredns/Corefile"},
						Resources: r.WorkspaceDNS.Resources,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: new(false),
							ReadOnlyRootFilesystem:   new(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						ReadinessProbe: probe(
							"/ready",
							workspaceDNSReadyPort,
						),
						LivenessProbe: probe("/health", workspaceDNSHealthPort),
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: "/etc/coredns", ReadOnly: true},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name:      "config",
						ConfigMap: &corev1.ConfigMapVolumeSource{Name: workspaceDNSName},
					},
				},
			}},
		},
	}
}

func (r *LocoResourceReconciler) applyWorkspaceDNSObject(
	ctx context.Context,
	namespace *corev1.Namespace,
	desired client.Object,
) error {
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(desired)
	if err != nil {
		return fmt.Errorf("encode workspace DNS object: %w", err)
	}
	delete(raw, "status")
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(desired.GetObjectKind().GroupVersionKind())
	key := client.ObjectKeyFromObject(desired)
	err = r.apiReader().Get(ctx, key, current)
	if apierrors.IsNotFound(err) {
		if createErr := r.Create(ctx, desired); createErr != nil {
			return fmt.Errorf(
				"create workspace DNS %s %s: %w",
				desired.GetObjectKind().GroupVersionKind().Kind,
				key,
				createErr,
			)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read workspace DNS object %s: %w", key, err)
	}
	owner := metav1.GetControllerOf(current)
	if owner == nil || owner.UID != namespace.UID || owner.Kind != "Namespace" || owner.APIVersion != "v1" ||
		owner.Name != namespace.Name ||
		current.GetLabels()[managed.LabelManagedBy] != managed.ManagedByValue ||
		current.GetLabels()[managed.LabelComponent] != componentWorkspaceDNS {
		return fmt.Errorf("%w: %s %s", errWorkspaceDNSCollision, current.GetKind(), key)
	}
	object := &unstructured.Unstructured{Object: raw}
	object.SetResourceVersion(current.GetResourceVersion())
	data, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("marshal workspace DNS object: %w", err)
	}
	patch := client.RawPatch(types.ApplyPatchType, data)
	if err := r.Patch(ctx, current, patch, client.FieldOwner(managed.FieldOwner), client.ForceOwnership); err != nil {
		return fmt.Errorf("apply workspace DNS object %s: %w", key, err)
	}
	return nil
}

func (c WorkspaceDNSConfig) podDNS(namespace, address string) *corev1ac.PodDNSConfigApplyConfiguration {
	option := corev1ac.PodDNSConfigOption().WithName("ndots").WithValue(workspaceDNSNdots)
	return corev1ac.PodDNSConfig().
		WithNameservers(address).
		WithSearches(namespace+".svc."+c.ClusterDomain, "svc."+c.ClusterDomain, c.ClusterDomain).
		WithOptions(option)
}
