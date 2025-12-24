/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/controller/api/v1alpha1"
)

// LocoResourceReconciler reconciles a LocoResource object
type LocoResourceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=loco.loco.deploy-app.com,resources=locoresources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=loco.loco.deploy-app.com,resources=locoresources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=loco.loco.deploy-app.com,resources=locoresources/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;create;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;create;list;watch;patch;update
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;create;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;create;list;watch;patch;update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;create;list;watch;patch;update
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;create;list;watch;patch;update
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;create;list;watch;patch;update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *LocoResourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	slog.InfoContext(ctx, "reconciling locoresource", "namespace", req.Namespace, "name", req.Name)

	// fetch the LocoResource
	var locoRes locov1alpha1.LocoResource
	if err := r.Get(ctx, req.NamespacedName, &locoRes); err != nil {
		slog.ErrorContext(ctx, "unable to fetch LocoResource", "error", err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// initialize status
	status := locoRes.Status
	if status.Phase == "" {
		status.Phase = "Deploying"
		now := metav1.Now()
		status.StartedAt = &now
	}

	// begin reconcile steps - these functions allocate and ensure Kubernetes resources
	if err := ensureNamespace(ctx, r.Client, &locoRes); err != nil {
		log.Error(err, "failed to ensure namespace")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure namespace: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	if err := ensureSecrets(ctx, r.Client, &locoRes); err != nil {
		log.Error(err, "failed to ensure secrets")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure secrets: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	if err := ensureServiceAccount(ctx, r.Client, &locoRes); err != nil {
		log.Error(err, "failed to ensure service account")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure service account: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	if err := ensureRoleAndBinding(ctx, r.Client, &locoRes); err != nil {
		log.Error(err, "failed to ensure role & binding")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure role & binding: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	dep, err := ensureDeployment(ctx, r.Client, &locoRes)
	if err != nil {
		log.Error(err, "failed to ensure deployment")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure deployment: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	if err := ensureService(ctx, r.Client, &locoRes); err != nil {
		log.Error(err, "failed to ensure service")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure service: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	if err := ensureHTTPRoute(ctx, r.Client, &locoRes); err != nil {
		log.Error(err, "failed to ensure HTTP route")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure HTTP route: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	// aggregate deployment status into our status
	if dep != nil {
		replicas := int32(1)
		if dep.Status.ReadyReplicas < replicas {
			status.Phase = "Deploying"
		} else {
			status.Phase = "Ready"
		}
	}

	status.ErrorMessage = ""
	now := metav1.Now()
	status.UpdatedAt = &now
	if status.Phase == "Ready" {
		status.CompletedAt = &now
	}

	r.updateLocoResourceStatus(ctx, &locoRes, &status)

	slog.InfoContext(ctx, "reconcile complete", "phase", status.Phase)
	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

// getName derives the app name from the LocoResource
func getName(locoRes *locov1alpha1.LocoResource) string {
	return fmt.Sprintf("resource-%d", locoRes.Spec.ResourceId)
}

// getNamespace derives the namespace from the LocoResource
func getNamespace(locoRes *locov1alpha1.LocoResource) string {
	return fmt.Sprintf("wks-res-%d", locoRes.Spec.ResourceId)
}

// ensureNamespace ensures the application namespace exists and is configured
func ensureNamespace(ctx context.Context, kubeClient client.Client, locoRes *locov1alpha1.LocoResource) error {
	namespace := getNamespace(locoRes)
	slog.InfoContext(ctx, "ensuring namespace", "namespace", namespace)

	ns := &corev1.Namespace{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Name: namespace}, ns); err == nil {
		slog.InfoContext(ctx, "namespace already exists", "namespace", namespace)
		return nil
	}

	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Labels: map[string]string{
				"loco.dev/app": "true",
			},
		},
	}

	if err := kubeClient.Create(ctx, ns); err != nil {
		slog.ErrorContext(ctx, "failed to create namespace", "namespace", namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "namespace created", "namespace", namespace)
	return nil
}

// ensureSecrets ensures all required secrets exist in the app namespace
func ensureSecrets(ctx context.Context, kubeClient client.Client, locoRes *locov1alpha1.LocoResource) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	slog.InfoContext(ctx, "ensuring secrets", "namespace", namespace, "name", name)

	// check if env secret already exists
	envSecret := &corev1.Secret{}
	envSecretName := fmt.Sprintf("%s-env", name)
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: envSecretName}, envSecret); err == nil {
		slog.InfoContext(ctx, "env secret already exists", "name", envSecretName, "namespace", namespace)
	} else {
		// create env secret from deployment env map
		secretData := make(map[string][]byte)
		if locoRes.Spec.ServiceSpec != nil && locoRes.Spec.ServiceSpec.Deployment != nil {
			for k, v := range locoRes.Spec.ServiceSpec.Deployment.Env {
				secretData[k] = []byte(v)
			}
		}

		envSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      envSecretName,
				Namespace: namespace,
				Labels: map[string]string{
					"app": name,
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: secretData,
		}

		if err := kubeClient.Create(ctx, envSecret); err != nil {
			slog.ErrorContext(ctx, "failed to create env secret", "name", envSecretName, "namespace", namespace, "error", err)
			return err
		}

		slog.InfoContext(ctx, "env secret created", "name", envSecretName, "namespace", namespace)
	}

	return nil
}

// ensureServiceAccount ensures the service account exists for the deployment
func ensureServiceAccount(ctx context.Context, kubeClient client.Client, locoRes *locov1alpha1.LocoResource) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	slog.InfoContext(ctx, "ensuring service account", "namespace", namespace, "name", name)

	sa := &corev1.ServiceAccount{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sa); err == nil {
		slog.InfoContext(ctx, "service account already exists", "name", name, "namespace", namespace)
		return nil
	}

	sa = &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	if err := kubeClient.Create(ctx, sa); err != nil {
		slog.ErrorContext(ctx, "failed to create service account", "name", name, "namespace", namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "service account created", "name", name, "namespace", namespace)
	return nil
}

// ensureRoleAndBinding ensures the RBAC role and role binding exist
func ensureRoleAndBinding(ctx context.Context, kubeClient client.Client, locoRes *locov1alpha1.LocoResource) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	slog.InfoContext(ctx, "ensuring role and role binding", "namespace", namespace, "name", name)

	envSecretName := fmt.Sprintf("%s-env", name)
	roleName := fmt.Sprintf("%s-role", name)
	roleBindingName := fmt.Sprintf("%s-binding", name)

	// check if role already exists
	role := &rbacv1.Role{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: roleName}, role); err == nil {
		slog.InfoContext(ctx, "role already exists", "name", roleName, "namespace", namespace)
	} else {
		// create role with read access to the env secret
		role = &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roleName,
				Namespace: namespace,
				Labels: map[string]string{
					"app": name,
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups:     []string{""},
					Resources:     []string{"secrets"},
					Verbs:         []string{"get", "list", "watch"},
					ResourceNames: []string{envSecretName},
				},
			},
		}

		if err := kubeClient.Create(ctx, role); err != nil {
			slog.ErrorContext(ctx, "failed to create role", "name", roleName, "namespace", namespace, "error", err)
			return err
		}

		slog.InfoContext(ctx, "role created", "name", roleName, "namespace", namespace)
	}

	// check if role binding already exists
	binding := &rbacv1.RoleBinding{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: roleBindingName}, binding); err == nil {
		slog.InfoContext(ctx, "role binding already exists", "name", roleBindingName, "namespace", namespace)
		return nil
	}

	// create role binding
	binding = &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleBindingName,
			Namespace: namespace,
			Labels: map[string]string{
				"app": name,
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      name,
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			Name:     roleName,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	if err := kubeClient.Create(ctx, binding); err != nil {
		slog.ErrorContext(ctx, "failed to create role binding", "name", roleBindingName, "namespace", namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "role binding created", "name", roleBindingName, "namespace", namespace)
	return nil
}

// ensureService ensures the Kubernetes service exists for the deployment
func ensureService(ctx context.Context, kubeClient client.Client, locoRes *locov1alpha1.LocoResource) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	slog.InfoContext(ctx, "ensuring service", "namespace", namespace, "name", name)

	svc := &corev1.Service{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, svc); err == nil {
		slog.InfoContext(ctx, "service already exists", "name", name, "namespace", namespace)
		return nil
	}

	svc = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": name,
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"app": name,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt32(8080),
				},
			},
		},
	}

	if err := kubeClient.Create(ctx, svc); err != nil {
		slog.ErrorContext(ctx, "failed to create service", "name", name, "namespace", namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "service created", "name", name, "namespace", namespace)
	return nil
}

// ensureDeployment ensures the Kubernetes deployment exists and is configured with the spec
// Returns the deployment if it exists or was created, or nil if skipped
func ensureDeployment(ctx context.Context, kubeClient client.Client, locoRes *locov1alpha1.LocoResource) (*appsv1.Deployment, error) {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	image := ""
	replicas := int32(1)
	var envVars []corev1.EnvVar
	var livenessProbe *corev1.Probe
	var readinessProbe *corev1.Probe
	var containerPort int32 = 8080
	cpuRequest := "100m"
	cpuLimit := "500m"
	memoryRequest := "128Mi"
	memoryLimit := "512Mi"

	if locoRes.Spec.ServiceSpec != nil && locoRes.Spec.ServiceSpec.Deployment != nil {
		image = locoRes.Spec.ServiceSpec.Deployment.Image
		for k, v := range locoRes.Spec.ServiceSpec.Deployment.Env {
			envVars = append(envVars, corev1.EnvVar{
				Name:  k,
				Value: v,
			})
		}
	}

	// prefer routing port, fall back to deployment port
	if locoRes.Spec.ServiceSpec != nil && locoRes.Spec.ServiceSpec.Routing != nil && locoRes.Spec.ServiceSpec.Routing.Port > 0 {
		containerPort = locoRes.Spec.ServiceSpec.Routing.Port
	} else if locoRes.Spec.ServiceSpec != nil && locoRes.Spec.ServiceSpec.Deployment != nil {
		containerPort = locoRes.Spec.ServiceSpec.Deployment.Port
	}

	if locoRes.Spec.ServiceSpec != nil && locoRes.Spec.ServiceSpec.Deployment != nil {
		if locoRes.Spec.ServiceSpec.Deployment.HealthCheck != nil {
			hc := locoRes.Spec.ServiceSpec.Deployment.HealthCheck
			probe := &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: hc.Path,
						Port: intstr.FromInt(int(containerPort)),
					},
				},
				InitialDelaySeconds: hc.StartupGracePeriod,
				TimeoutSeconds:      hc.Timeout,
				PeriodSeconds:       hc.Interval,
				FailureThreshold:    hc.FailThreshold,
			}

			livenessProbe = probe
			readinessProbe = probe
		}
	}

	if locoRes.Spec.ServiceSpec != nil && locoRes.Spec.ServiceSpec.Resources != nil {
		if locoRes.Spec.ServiceSpec.Resources.CPU != "" {
			cpuRequest = locoRes.Spec.ServiceSpec.Resources.CPU
			cpuLimit = locoRes.Spec.ServiceSpec.Resources.CPU
		}
		if locoRes.Spec.ServiceSpec.Resources.Memory != "" {
			memoryRequest = locoRes.Spec.ServiceSpec.Resources.Memory
			memoryLimit = locoRes.Spec.ServiceSpec.Resources.Memory
		}
		if locoRes.Spec.ServiceSpec.Resources.Replicas.Min > 0 {
			replicas = locoRes.Spec.ServiceSpec.Resources.Replicas.Min
		}
	}

	slog.InfoContext(ctx, "ensuring deployment", "namespace", namespace, "name", name, "replicas", replicas, "image", image)

	dep := &appsv1.Deployment{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, dep); err == nil {
		slog.InfoContext(ctx, "deployment already exists", "name", name, "namespace", namespace)
		return dep, nil
	}

	container := corev1.Container{
		Name:  name,
		Image: image,
		Env:   envVars,
		Ports: []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: containerPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpuRequest),
				corev1.ResourceMemory: resource.MustParse(memoryRequest),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpuLimit),
				corev1.ResourceMemory: resource.MustParse(memoryLimit),
			},
		},
	}

	if livenessProbe != nil {
		container.LivenessProbe = livenessProbe
		container.ReadinessProbe = readinessProbe
	}

	dep = &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": name,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": name,
				},
			},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxSurge:       &intstr.IntOrString{Type: intstr.String, StrVal: "25%"},
					MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "25%"},
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": name,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: name,
					RestartPolicy:      corev1.RestartPolicyAlways,
					Containers: []corev1.Container{
						container,
					},
				},
			},
		},
	}

	if err := kubeClient.Create(ctx, dep); err != nil {
		slog.ErrorContext(ctx, "failed to create deployment", "name", name, "namespace", namespace, "error", err)
		return nil, err
	}

	slog.InfoContext(ctx, "deployment created", "name", name, "namespace", namespace, "replicas", replicas)
	return dep, nil
}

// ensureHTTPRoute ensures the HTTPRoute exists for traffic ingress (Envoy Gateway)
func ensureHTTPRoute(ctx context.Context, kubeClient client.Client, locoRes *locov1alpha1.LocoResource) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)

	slog.InfoContext(ctx, "ensuring HTTPRoute", "namespace", namespace, "name", name)

	routeName := fmt.Sprintf("%s-route", name)
	route := &v1Gateway.HTTPRoute{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: routeName}, route); err == nil {
		slog.InfoContext(ctx, "HTTPRoute already exists", "name", routeName, "namespace", namespace)
		return nil
	}

	// create HTTPRoute
	// parent gateway must exist in loco-gateway namespace
	pathType := v1Gateway.PathMatchPathPrefix
	gatewayNamespace := "loco-gateway"
	pathValue := "/"
	var backendPort *v1Gateway.PortNumber

	if locoRes.Spec.ServiceSpec != nil && locoRes.Spec.ServiceSpec.Routing != nil {
		if locoRes.Spec.ServiceSpec.Routing.PathPrefix != "" {
			pathValue = locoRes.Spec.ServiceSpec.Routing.PathPrefix
		}
		if locoRes.Spec.ServiceSpec.Routing.Port > 0 {
			backendPort = ptrToPortNumber(int(locoRes.Spec.ServiceSpec.Routing.Port))
		}
	}

	if backendPort == nil {
		backendPort = ptrToPortNumber(80)
	}

	route = &v1Gateway.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: namespace,
			Labels: map[string]string{
				"app": name,
			},
		},
		Spec: v1Gateway.HTTPRouteSpec{
			CommonRouteSpec: v1Gateway.CommonRouteSpec{
				ParentRefs: []v1Gateway.ParentReference{
					{
						Name:      v1Gateway.ObjectName("loco-gateway"),
						Namespace: (*v1Gateway.Namespace)(&gatewayNamespace),
					},
				},
			},
			Rules: []v1Gateway.HTTPRouteRule{
				{
					Matches: []v1Gateway.HTTPRouteMatch{
						{
							Path: &v1Gateway.HTTPPathMatch{
								Type:  &pathType,
								Value: ptrToString(pathValue),
							},
						},
					},
					BackendRefs: []v1Gateway.HTTPBackendRef{
						{
							BackendRef: v1Gateway.BackendRef{
								BackendObjectReference: v1Gateway.BackendObjectReference{
									Name: v1Gateway.ObjectName(name),
									Port: backendPort,
									Kind: ptrToKind("Service"),
								},
							},
						},
					},
				},
			},
		},
	}

	if err := kubeClient.Create(ctx, route); err != nil {
		slog.ErrorContext(ctx, "failed to create HTTPRoute", "name", routeName, "namespace", namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "HTTPRoute created", "name", routeName, "namespace", namespace)
	return nil
}

// updateLocoResourceStatus writes the observed status back to the LocoResource status subresource
func (r *LocoResourceReconciler) updateLocoResourceStatus(
	ctx context.Context,
	locoRes *locov1alpha1.LocoResource,
	status *locov1alpha1.LocoResourceStatus,
) {
	locoRes.Status = *status
	if err := r.Status().Update(ctx, locoRes); err != nil {
		slog.ErrorContext(ctx, "failed to update LocoResource status", "error", err)
	}
}

// ptrToPortNumber returns a pointer to a PortNumber
func ptrToPortNumber(p int) *v1Gateway.PortNumber {
	n := v1Gateway.PortNumber(p)
	return &n
}

// ptrToString returns a pointer to a string
func ptrToString(s string) *string {
	return &s
}

// ptrToKind returns a pointer to a Kind
func ptrToKind(k string) *v1Gateway.Kind {
	t := v1Gateway.Kind(k)
	return &t
}

// SetupWithManager sets up the controller with the Manager.
func (r *LocoResourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&locov1alpha1.LocoResource{}).
		Named("locoresource").
		Complete(r)
}
