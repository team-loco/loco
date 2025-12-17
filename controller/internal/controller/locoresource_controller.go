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

	// derive the app spec we need to provision
	appSpec := deriveAppSpec(&locoRes)

	// begin reconcile steps - these functions allocate and ensure Kubernetes resources
	if err := ensureNamespace(ctx, r.Client, appSpec); err != nil {
		log.Error(err, "failed to ensure namespace")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure namespace: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	if err := ensureSecrets(ctx, r.Client, appSpec); err != nil {
		log.Error(err, "failed to ensure secrets")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure secrets: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	if err := ensureServiceAccount(ctx, r.Client, appSpec); err != nil {
		log.Error(err, "failed to ensure service account")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure service account: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	if err := ensureRoleAndBinding(ctx, r.Client, appSpec); err != nil {
		log.Error(err, "failed to ensure role & binding")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure role & binding: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	dep, err := ensureDeployment(ctx, r.Client, appSpec)
	if err != nil {
		log.Error(err, "failed to ensure deployment")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure deployment: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	if err := ensureService(ctx, r.Client, appSpec); err != nil {
		log.Error(err, "failed to ensure service")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure service: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	if err := ensureHTTPRoute(ctx, r.Client, appSpec); err != nil {
		log.Error(err, "failed to ensure HTTP route")
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure HTTP route: %v", err)
		r.updateLocoResourceStatus(ctx, &locoRes, &status)
		return ctrl.Result{}, err
	}

	// aggregate deployment status into our status
	if dep != nil {
		if dep.Status.ReadyReplicas < appSpec.Replicas {
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

// deriveAppSpec converts LocoResource into an app spec for provisioning
func deriveAppSpec(locoRes *locov1alpha1.LocoResource) *appSpec {
	spec := &appSpec{
		Name:      locoRes.Spec.App.Name,
		Namespace: fmt.Sprintf("wks-app-%d", locoRes.Spec.App.AppId),
		Expose:    true,
	}

	// from deployment spec (if present)
	if locoRes.Spec.Deployment != nil {
		spec.Image = locoRes.Spec.Deployment.Image
		if locoRes.Spec.Deployment.InitialReplicas > 0 {
			spec.Replicas = locoRes.Spec.Deployment.InitialReplicas
		}
		if len(locoRes.Spec.Deployment.Env) > 0 {
			spec.Env = locoRes.Spec.Deployment.Env
		}
		spec.CPU = locoRes.Spec.Deployment.Resources.CPU
		spec.Memory = locoRes.Spec.Deployment.Resources.Memory
	}

	// from app spec (policy/intent)
	spec.EnableHPA = locoRes.Spec.Deployment.Resources.Scalers.Enabled
	spec.MinReplicas = locoRes.Spec.Deployment.Resources.Replicas.Min
	spec.MaxReplicas = locoRes.Spec.Deployment.Resources.Replicas.Max
	spec.CPUThreshold = locoRes.Spec.Deployment.Resources.Scalers.CPUTarget

	return spec
}

// appSpec represents the app configuration for provisioning
type appSpec struct {
	Name         string
	Namespace    string
	Image        string
	Replicas     int32
	EnableHPA    bool
	MinReplicas  int32
	MaxReplicas  int32
	CPUThreshold int32
	CPU          string
	Memory       string
	Env          map[string]string
	Expose       bool
}

// ensureNamespace ensures the application namespace exists and is configured
func ensureNamespace(ctx context.Context, kubeClient client.Client, spec *appSpec) error {
	slog.InfoContext(ctx, "ensuring namespace", "namespace", spec.Namespace)

	ns := &corev1.Namespace{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Name: spec.Namespace}, ns); err == nil {
		slog.InfoContext(ctx, "namespace already exists", "namespace", spec.Namespace)
		return nil
	}

	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: spec.Namespace,
			Labels: map[string]string{
				"expose-via-gw": "true",
				"loco.dev/app":  "true",
			},
		},
	}

	if err := kubeClient.Create(ctx, ns); err != nil {
		slog.ErrorContext(ctx, "failed to create namespace", "namespace", spec.Namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "namespace created", "namespace", spec.Namespace)
	return nil
}

// ensureSecrets ensures all required secrets exist in the app namespace
func ensureSecrets(ctx context.Context, kubeClient client.Client, spec *appSpec) error {
	slog.InfoContext(ctx, "ensuring secrets", "namespace", spec.Namespace, "name", spec.Name)

	// check if env secret already exists
	envSecret := &corev1.Secret{}
	envSecretName := fmt.Sprintf("%s-env", spec.Name)
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: spec.Namespace, Name: envSecretName}, envSecret); err == nil {
		slog.InfoContext(ctx, "env secret already exists", "name", envSecretName, "namespace", spec.Namespace)
	} else {
		// create env secret from spec.Env map
		secretData := make(map[string][]byte)
		for k, v := range spec.Env {
			secretData[k] = []byte(v)
		}

		envSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      envSecretName,
				Namespace: spec.Namespace,
				Labels: map[string]string{
					"app": spec.Name,
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: secretData,
		}

		if err := kubeClient.Create(ctx, envSecret); err != nil {
			slog.ErrorContext(ctx, "failed to create env secret", "name", envSecretName, "namespace", spec.Namespace, "error", err)
			return err
		}

		slog.InfoContext(ctx, "env secret created", "name", envSecretName, "namespace", spec.Namespace)
	}

	return nil
}

// ensureServiceAccount ensures the service account exists for the deployment
func ensureServiceAccount(ctx context.Context, kubeClient client.Client, spec *appSpec) error {
	slog.InfoContext(ctx, "ensuring service account", "namespace", spec.Namespace, "name", spec.Name)

	sa := &corev1.ServiceAccount{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: spec.Namespace, Name: spec.Name}, sa); err == nil {
		slog.InfoContext(ctx, "service account already exists", "name", spec.Name, "namespace", spec.Namespace)
		return nil
	}

	sa = &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
		},
	}

	if err := kubeClient.Create(ctx, sa); err != nil {
		slog.ErrorContext(ctx, "failed to create service account", "name", spec.Name, "namespace", spec.Namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "service account created", "name", spec.Name, "namespace", spec.Namespace)
	return nil
}

// ensureRoleAndBinding ensures the RBAC role and role binding exist
func ensureRoleAndBinding(ctx context.Context, kubeClient client.Client, spec *appSpec) error {
	slog.InfoContext(ctx, "ensuring role and role binding", "namespace", spec.Namespace, "name", spec.Name)

	envSecretName := fmt.Sprintf("%s-env", spec.Name)
	roleName := fmt.Sprintf("%s-role", spec.Name)
	roleBindingName := fmt.Sprintf("%s-binding", spec.Name)

	// check if role already exists
	role := &rbacv1.Role{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: spec.Namespace, Name: roleName}, role); err == nil {
		slog.InfoContext(ctx, "role already exists", "name", roleName, "namespace", spec.Namespace)
	} else {
		// create role with read access to the env secret
		role = &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roleName,
				Namespace: spec.Namespace,
				Labels: map[string]string{
					"app": spec.Name,
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
			slog.ErrorContext(ctx, "failed to create role", "name", roleName, "namespace", spec.Namespace, "error", err)
			return err
		}

		slog.InfoContext(ctx, "role created", "name", roleName, "namespace", spec.Namespace)
	}

	// check if role binding already exists
	binding := &rbacv1.RoleBinding{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: spec.Namespace, Name: roleBindingName}, binding); err == nil {
		slog.InfoContext(ctx, "role binding already exists", "name", roleBindingName, "namespace", spec.Namespace)
		return nil
	}

	// create role binding
	binding = &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleBindingName,
			Namespace: spec.Namespace,
			Labels: map[string]string{
				"app": spec.Name,
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      spec.Name,
				Namespace: spec.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			Name:     roleName,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	if err := kubeClient.Create(ctx, binding); err != nil {
		slog.ErrorContext(ctx, "failed to create role binding", "name", roleBindingName, "namespace", spec.Namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "role binding created", "name", roleBindingName, "namespace", spec.Namespace)
	return nil
}

// ensureService ensures the Kubernetes service exists for the deployment
func ensureService(ctx context.Context, kubeClient client.Client, spec *appSpec) error {
	slog.InfoContext(ctx, "ensuring service", "namespace", spec.Namespace, "name", spec.Name)

	svc := &corev1.Service{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: spec.Namespace, Name: spec.Name}, svc); err == nil {
		slog.InfoContext(ctx, "service already exists", "name", spec.Name, "namespace", spec.Namespace)
		return nil
	}

	svc = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels: map[string]string{
				"app": spec.Name,
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"app": spec.Name,
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
		slog.ErrorContext(ctx, "failed to create service", "name", spec.Name, "namespace", spec.Namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "service created", "name", spec.Name, "namespace", spec.Namespace)
	return nil
}

// ensureDeployment ensures the Kubernetes deployment exists and is configured with the spec
// Returns the deployment if it exists or was created, or nil if skipped
func ensureDeployment(ctx context.Context, kubeClient client.Client, spec *appSpec) (*appsv1.Deployment, error) {
	slog.InfoContext(ctx, "ensuring deployment", "namespace", spec.Namespace, "name", spec.Name, "replicas", spec.Replicas, "image", spec.Image)

	dep := &appsv1.Deployment{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: spec.Namespace, Name: spec.Name}, dep); err == nil {
		slog.InfoContext(ctx, "deployment already exists", "name", spec.Name, "namespace", spec.Namespace)
		return dep, nil
	}

	replicas := spec.Replicas
	if replicas == 0 {
		replicas = 1
	}

	envVars := make([]corev1.EnvVar, 0, len(spec.Env))
	for k, v := range spec.Env {
		envVars = append(envVars, corev1.EnvVar{
			Name:  k,
			Value: v,
		})
	}

	// default resource values if not specified
	cpuRequest := "100m"
	cpuLimit := "500m"
	memoryRequest := "128Mi"
	memoryLimit := "512Mi"

	if spec.CPU != "" {
		cpuRequest = spec.CPU
		cpuLimit = spec.CPU
	}
	if spec.Memory != "" {
		memoryRequest = spec.Memory
		memoryLimit = spec.Memory
	}

	dep = &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.Name,
			Namespace: spec.Namespace,
			Labels: map[string]string{
				"app": spec.Name,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": spec.Name,
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
						"app": spec.Name,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: spec.Name,
					RestartPolicy:      corev1.RestartPolicyAlways,
					Containers: []corev1.Container{
						{
							Name:  spec.Name,
							Image: spec.Image,
							Env:   envVars,
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: 8080,
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
						},
					},
				},
			},
		},
	}

	if err := kubeClient.Create(ctx, dep); err != nil {
		slog.ErrorContext(ctx, "failed to create deployment", "name", spec.Name, "namespace", spec.Namespace, "error", err)
		return nil, err
	}

	slog.InfoContext(ctx, "deployment created", "name", spec.Name, "namespace", spec.Namespace, "replicas", replicas)
	return dep, nil
}

// ensureHTTPRoute ensures the HTTPRoute exists for traffic ingress (Envoy Gateway)
func ensureHTTPRoute(ctx context.Context, kubeClient client.Client, spec *appSpec) error {
	if !spec.Expose {
		slog.InfoContext(ctx, "skipping HTTPRoute creation, expose is false", "name", spec.Name)
		return nil
	}

	slog.InfoContext(ctx, "ensuring HTTPRoute", "namespace", spec.Namespace, "name", spec.Name)

	routeName := fmt.Sprintf("%s-route", spec.Name)
	route := &v1Gateway.HTTPRoute{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: spec.Namespace, Name: routeName}, route); err == nil {
		slog.InfoContext(ctx, "HTTPRoute already exists", "name", routeName, "namespace", spec.Namespace)
		return nil
	}

	// create HTTPRoute
	// parent gateway must exist in loco-gateway namespace
	pathType := v1Gateway.PathMatchPathPrefix
	gatewayNamespace := "loco-gateway"

	route = &v1Gateway.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: spec.Namespace,
			Labels: map[string]string{
				"app": spec.Name,
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
								Value: ptrToString("/"),
							},
						},
					},
					BackendRefs: []v1Gateway.HTTPBackendRef{
						{
							BackendRef: v1Gateway.BackendRef{
								BackendObjectReference: v1Gateway.BackendObjectReference{
									Name: v1Gateway.ObjectName(spec.Name),
									Port: ptrToPortNumber(80),
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
		slog.ErrorContext(ctx, "failed to create HTTPRoute", "name", routeName, "namespace", spec.Namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "HTTPRoute created", "name", routeName, "namespace", spec.Namespace)
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
