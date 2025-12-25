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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/controller/api/v1alpha1"
)

// todo: finalize on the domain we wanna use inside kubernetes.
const (
	finalizerSecretRefresher = "loco.dev/secret-refresher"
)

// LocoResourceReconciler reconciles a LocoResource object
type LocoResourceReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// needed for refreshing container image token.
	gitlabURL         string
	gitlabPAT         string
	gitlabProjectID   string
	gitlabRegistryURL string
	secretRefreshers  map[string]context.CancelFunc

	// reconcile can be called concurrently, so protect map access.
	secretRefreshersMux sync.Mutex
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
	slog.InfoContext(ctx, "reconciling locoresource", "namespace", req.Namespace, "name", req.Name)

	// fetch the LocoResource
	var locoRes locov1alpha1.LocoResource
	if err := r.Get(ctx, req.NamespacedName, &locoRes); err != nil {
		slog.ErrorContext(ctx, "unable to fetch LocoResource", "error", err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// validate spec early to prevent nil panics
	if err := r.validateLocoResource(&locoRes); err != nil {
		slog.ErrorContext(ctx, "invalid LocoResource spec", "error", err)
		status := locoRes.Status
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("validation failed: %v", err)
		if statusErr := r.updateLRStatus(ctx, &locoRes, &status); statusErr != nil {
			slog.ErrorContext(ctx, "failed to update status after validation error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	// deletion timestamp means we need to clean up
	if locoRes.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, &locoRes)
	}

	// ensure finalizer
	if !controllerutil.ContainsFinalizer(&locoRes, finalizerSecretRefresher) {
		controllerutil.AddFinalizer(&locoRes, finalizerSecretRefresher)
		if err := r.Update(ctx, &locoRes); err != nil {
			slog.ErrorContext(ctx, "failed to add finalizer", "error", err)
			return ctrl.Result{}, err
		}
		slog.InfoContext(ctx, "added finalizer", "finalizer", finalizerSecretRefresher)
	}

	// initialize status
	status := locoRes.Status
	slog.InfoContext(ctx, "initializing status", "phase", status.Phase)
	if status.Phase == "" {
		status.Phase = "Deploying"
		now := metav1.Now()
		status.StartedAt = &now
	}

	// begin reconcile steps - these functions allocate and ensure Kubernetes resources
	if err := ensureNamespace(ctx, r.Client, &locoRes); err != nil {
		slog.ErrorContext(ctx, "failed to ensure namespace", "error", err)
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure namespace: %v", err)
		if statusErr := r.updateLRStatus(ctx, &locoRes, &status); statusErr != nil {
			slog.ErrorContext(ctx, "failed to update status after namespace error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	if err := ensureEnvSecret(ctx, r.Client, &locoRes); err != nil {
		slog.ErrorContext(ctx, "failed to ensure secrets", "error", err)
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure secrets: %v", err)
		if statusErr := r.updateLRStatus(ctx, &locoRes, &status); statusErr != nil {
			slog.ErrorContext(ctx, "failed to update status after secrets error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	if err := r.ensureImagePullSecret(ctx, &locoRes); err != nil {
		slog.ErrorContext(ctx, "failed to ensure image pull secret", "error", err)
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure image pull secret: %v", err)
		if statusErr := r.updateLRStatus(ctx, &locoRes, &status); statusErr != nil {
			slog.ErrorContext(ctx, "failed to update status after image pull secret error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	if err := r.ensureServiceAccount(ctx, &locoRes); err != nil {
		slog.ErrorContext(ctx, "failed to ensure service account", "error", err)
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure service account: %v", err)
		if statusErr := r.updateLRStatus(ctx, &locoRes, &status); statusErr != nil {
			slog.ErrorContext(ctx, "failed to update status after service account error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	if err := r.ensureRoleAndBinding(ctx, &locoRes); err != nil {
		slog.ErrorContext(ctx, "failed to ensure role & binding", "error", err)
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure role & binding: %v", err)
		if statusErr := r.updateLRStatus(ctx, &locoRes, &status); statusErr != nil {
			slog.ErrorContext(ctx, "failed to update status after role & binding error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	dep, err := r.ensureDeployment(ctx, &locoRes)
	if err != nil {
		slog.ErrorContext(ctx, "failed to ensure deployment", "error", err)
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure deployment: %v", err)
		if statusErr := r.updateLRStatus(ctx, &locoRes, &status); statusErr != nil {
			slog.ErrorContext(ctx, "failed to update status after deployment error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	if err := r.ensureService(ctx, &locoRes); err != nil {
		slog.ErrorContext(ctx, "failed to ensure service", "error", err)
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure service: %v", err)
		if statusErr := r.updateLRStatus(ctx, &locoRes, &status); statusErr != nil {
			slog.ErrorContext(ctx, "failed to update status after service error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	if err := r.ensureHTTPRoute(ctx, &locoRes); err != nil {
		slog.ErrorContext(ctx, "failed to ensure HTTP route", "error", err)
		status.Phase = "Failed"
		status.ErrorMessage = fmt.Sprintf("failed to ensure HTTP route: %v", err)
		if statusErr := r.updateLRStatus(ctx, &locoRes, &status); statusErr != nil {
			slog.ErrorContext(ctx, "failed to update status after HTTP route error", "error", statusErr)
		}
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

	if err := r.updateLRStatus(ctx, &locoRes, &status); err != nil {
		slog.ErrorContext(ctx, "failed to update status after successful reconcile", "error", err)
		return ctrl.Result{}, err
	}

	r.startSecretRefresherGoroutine(ctx, &locoRes)

	slog.InfoContext(ctx, "reconcile complete", "phase", status.Phase)
	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

// handleDeletion cancels the secret refresher goroutine, deletes the namespace, and removes the finalizer
func (r *LocoResourceReconciler) handleDeletion(ctx context.Context, locoRes *locov1alpha1.LocoResource) (ctrl.Result, error) {
	namespace := getNamespace(locoRes)
	resourceKey := fmt.Sprintf("%s/%s", namespace, getName(locoRes))

	r.secretRefreshersMux.Lock()
	if cancel, exists := r.secretRefreshers[resourceKey]; exists {
		cancel()
		delete(r.secretRefreshers, resourceKey)
		slog.InfoContext(ctx, "canceled secret refresher goroutine", "resource", resourceKey)
	}
	r.secretRefreshersMux.Unlock()

	ns := &corev1.Namespace{}
	if err := r.Get(ctx, client.ObjectKey{Name: namespace}, ns); err == nil {
		slog.InfoContext(ctx, "deleting namespace", "namespace", namespace)
		if err := r.Delete(ctx, ns); err != nil {
			slog.ErrorContext(ctx, "failed to delete namespace", "namespace", namespace, "error", err)
			return ctrl.Result{}, err
		}
		slog.InfoContext(ctx, "namespace deleted", "namespace", namespace)
	}

	if controllerutil.ContainsFinalizer(locoRes, finalizerSecretRefresher) {
		controllerutil.RemoveFinalizer(locoRes, finalizerSecretRefresher)
		if err := r.Update(ctx, locoRes); err != nil {
			slog.ErrorContext(ctx, "failed to remove finalizer", "error", err)
			return ctrl.Result{}, err
		}
		slog.InfoContext(ctx, "removed finalizer", "finalizer", finalizerSecretRefresher)
	}

	return ctrl.Result{}, nil
}

// getName derives the app name from the LocoResource
func getName(locoRes *locov1alpha1.LocoResource) string {
	return fmt.Sprintf("resource-%d", locoRes.Spec.ResourceId)
}

// getNamespace derives the namespace from the LocoResource
func getNamespace(locoRes *locov1alpha1.LocoResource) string {
	return fmt.Sprintf("wks-%d-res-%d", locoRes.Spec.WorkspaceID, locoRes.Spec.ResourceId)
}

func getImageSecretName(locoRes *locov1alpha1.LocoResource) string {
	return fmt.Sprintf("%s-image-pull", getName(locoRes))
}

// getContainerPort extracts the container port from routing or deployment config
// Prefers routing port, falls back to deployment port, defaults to 8000
func getContainerPort(locoRes *locov1alpha1.LocoResource) int32 {
	if locoRes.Spec.ServiceSpec.Deployment.Port > 0 {
		return locoRes.Spec.ServiceSpec.Deployment.Port
	}
	slog.Warn("defaulting port to 8000")
	return 8000
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

// ensureEnvSecret ensures all required secrets exist in the app namespace
func ensureEnvSecret(ctx context.Context, kubeClient client.Client, locoRes *locov1alpha1.LocoResource) error {
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
		for k, v := range locoRes.Spec.ServiceSpec.Deployment.Env {
			secretData[k] = []byte(v)
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

// ensureImagePullSecret creates or updates the image pull secret for GitLab registry
func (r *LocoResourceReconciler) ensureImagePullSecret(ctx context.Context, locoRes *locov1alpha1.LocoResource) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	secretName := getImageSecretName(locoRes)

	slog.InfoContext(ctx, "ensuring image pull secret", "namespace", namespace, "name", secretName)

	secret := &corev1.Secret{}
	secretExists := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, secret) == nil

	if secretExists {
		expiryStr, ok := secret.Annotations["tokenExpiry"]
		if ok {
			expiryTime, err := time.Parse(time.RFC3339, expiryStr)
			if err == nil {
				timeUntilExpiry := time.Until(expiryTime)
				if timeUntilExpiry > 5*time.Minute {
					slog.DebugContext(ctx, "image pull secret token still valid", "namespace", namespace, "name", secretName, "timeUntilExpiry", timeUntilExpiry)
					return nil
				}
				slog.InfoContext(ctx, "image pull secret token expiring soon, refreshing", "namespace", namespace, "name", secretName, "timeUntilExpiry", timeUntilExpiry)
			}
		}
	}

	token, err := r.getGitlabDeployToken(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get gitlab deploy token", "error", err)
		return err
	}

	dockerConfig, err := buildDockerConfig(r.gitlabRegistryURL, token.Username, token.Token)
	if err != nil {
		slog.ErrorContext(ctx, "failed to build docker config", "error", err)
		return err
	}
	expiryTime := time.Now().Add(55 * time.Minute).UTC().Format(time.RFC3339)

	secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.Type = corev1.SecretTypeDockerConfigJson
		secret.Labels = map[string]string{
			"app": name,
		}
		secret.Annotations = map[string]string{
			"tokenExpiry": expiryTime,
		}
		secret.Data = map[string][]byte{
			".dockerconfigjson": dockerConfig,
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to ensure image pull secret", "name", secretName, "namespace", namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "image pull secret ensured", "name", secretName, "namespace", namespace, "op", op)
	return nil
}

// ensureServiceAccount ensures the service account exists for the deployment and references image pull secret
func (r *LocoResourceReconciler) ensureServiceAccount(ctx context.Context, locoRes *locov1alpha1.LocoResource) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	secretName := getImageSecretName(locoRes)

	slog.InfoContext(ctx, "ensuring service account", "namespace", namespace, "name", name)

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		hasImagePullSecret := false
		for _, ips := range sa.ImagePullSecrets {
			if ips.Name == secretName {
				hasImagePullSecret = true
				break
			}
		}

		if !hasImagePullSecret {
			sa.ImagePullSecrets = append(sa.ImagePullSecrets, corev1.LocalObjectReference{Name: secretName})
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to ensure service account", "name", name, "namespace", namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "service account ensured", "name", name, "namespace", namespace, "op", op)
	return nil
}

// ensureRoleAndBinding ensures the RBAC role and role binding exist
func (r *LocoResourceReconciler) ensureRoleAndBinding(ctx context.Context, locoRes *locov1alpha1.LocoResource) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	slog.InfoContext(ctx, "ensuring role and role binding", "namespace", namespace, "name", name)

	envSecretName := fmt.Sprintf("%s-env", name)
	roleName := fmt.Sprintf("%s-role", name)
	roleBindingName := fmt.Sprintf("%s-binding", name)

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleName,
			Namespace: namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Labels = map[string]string{
			"app": name,
		}
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get", "list", "watch"},
				ResourceNames: []string{envSecretName},
			},
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to ensure role", "name", roleName, "namespace", namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "role ensured", "name", roleName, "namespace", namespace, "op", op)

	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleBindingName,
			Namespace: namespace,
		},
	}

	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
		binding.Labels = map[string]string{
			"app": name,
		}
		binding.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      name,
				Namespace: namespace,
			},
		}
		binding.RoleRef = rbacv1.RoleRef{
			Kind:     "Role",
			Name:     roleName,
			APIGroup: "rbac.authorization.k8s.io",
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to ensure role binding", "name", roleBindingName, "namespace", namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "role binding ensured", "name", roleBindingName, "namespace", namespace, "op", op)
	return nil
}

// ensureService ensures the Kubernetes service exists for the deployment
func (r *LocoResourceReconciler) ensureService(ctx context.Context, locoRes *locov1alpha1.LocoResource) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	containerPort := getContainerPort(locoRes)

	slog.InfoContext(ctx, "ensuring service", "namespace", namespace, "name", name, "containerPort", containerPort)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Labels = map[string]string{
			"app": name,
		}
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		svc.Spec.Selector = map[string]string{
			"app": name,
		}
		svc.Spec.Ports = []corev1.ServicePort{
			{
				Name:       "http",
				Protocol:   corev1.ProtocolTCP,
				Port:       80,
				TargetPort: intstr.FromInt32(containerPort),
			},
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to ensure service", "name", name, "namespace", namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "service ensured", "name", name, "namespace", namespace, "op", op)
	return nil
}

// ensureDeployment ensures the Kubernetes deployment exists and is configured with the spec
// Returns the deployment if it exists or was created, or nil if skipped
func (r *LocoResourceReconciler) ensureDeployment(ctx context.Context, locoRes *locov1alpha1.LocoResource) (*appsv1.Deployment, error) {
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

	image = locoRes.Spec.ServiceSpec.Deployment.Image
	for k, v := range locoRes.Spec.ServiceSpec.Deployment.Env {
		envVars = append(envVars, corev1.EnvVar{
			Name:  k,
			Value: v,
		})
	}

	if locoRes.Spec.ServiceSpec.Deployment.Port > 0 {
		containerPort = locoRes.Spec.ServiceSpec.Deployment.Port
	}

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

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Labels = map[string]string{
			"app": name,
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

		dep.Spec.Replicas = &replicas
		dep.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": name,
			},
		}
		dep.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxSurge:       &intstr.IntOrString{Type: intstr.String, StrVal: "25%"},
				MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "25%"},
			},
		}
		dep.Spec.Template = corev1.PodTemplateSpec{
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
		}

		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to ensure deployment", "name", name, "namespace", namespace, "error", err)
		return nil, err
	}

	slog.InfoContext(ctx, "deployment ensured", "name", name, "namespace", namespace, "replicas", replicas, "op", op)
	return dep, nil
}

// ensureHTTPRoute ensures the HTTPRoute exists for traffic ingress (Envoy Gateway)
func (r *LocoResourceReconciler) ensureHTTPRoute(ctx context.Context, locoRes *locov1alpha1.LocoResource) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)

	slog.InfoContext(ctx, "ensuring HTTPRoute", "namespace", namespace, "name", name)

	routeName := fmt.Sprintf("%s-route", name)
	pathType := v1Gateway.PathMatchPathPrefix
	gatewayNamespace := "loco-gateway"
	pathValue := "/"
	var backendPort *v1Gateway.PortNumber

	if locoRes.Spec.ServiceSpec.Routing != nil {
		if locoRes.Spec.ServiceSpec.Routing.PathPrefix != "" {
			pathValue = locoRes.Spec.ServiceSpec.Routing.PathPrefix
		}
		if locoRes.Spec.ServiceSpec.Deployment.Port > 0 {
			backendPort = ptrToPortNumber(int(locoRes.Spec.ServiceSpec.Deployment.Port))
		}
	}

	if backendPort == nil {
		backendPort = ptrToPortNumber(80)
	}

	route := &v1Gateway.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, route, func() error {
		route.Labels = map[string]string{
			"app": name,
		}
		route.Spec.ParentRefs = []v1Gateway.ParentReference{
			{
				Name:      v1Gateway.ObjectName("loco-gateway"),
				Namespace: (*v1Gateway.Namespace)(&gatewayNamespace),
			},
		}
		route.Spec.Rules = []v1Gateway.HTTPRouteRule{
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
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to ensure HTTPRoute", "name", routeName, "namespace", namespace, "error", err)
		return err
	}

	slog.InfoContext(ctx, "HTTPRoute ensured", "name", routeName, "namespace", namespace, "op", op)
	return nil
}

// updateLRStatus writes the observed status back to the LocoResource status subresource
func (r *LocoResourceReconciler) updateLRStatus(
	ctx context.Context,
	locoRes *locov1alpha1.LocoResource,
	status *locov1alpha1.LocoResourceStatus,
) error {
	locoRes.Status = *status
	if err := r.Status().Update(ctx, locoRes); err != nil {
		slog.ErrorContext(ctx, "failed to update LocoResource status", "error", err)
		return err
	}
	return nil
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

// startSecretRefresherGoroutine starts a goroutine to refresh the image pull secret token
func (r *LocoResourceReconciler) startSecretRefresherGoroutine(ctx context.Context, locoRes *locov1alpha1.LocoResource) {
	resourceKey := fmt.Sprintf("%s/%s", getNamespace(locoRes), getName(locoRes))

	r.secretRefreshersMux.Lock()
	defer r.secretRefreshersMux.Unlock()

	if _, exists := r.secretRefreshers[resourceKey]; exists {
		return
	}

	refreshCtx, cancel := context.WithCancel(ctx)
	r.secretRefreshers[resourceKey] = cancel

	go r.secretRefresher(refreshCtx, locoRes, resourceKey)
}

// secretRefresher periodically refreshes the image pull secret token
func (r *LocoResourceReconciler) secretRefresher(ctx context.Context, locoRes *locov1alpha1.LocoResource, resourceKey string) {
	defer func() {
		r.secretRefreshersMux.Lock()
		delete(r.secretRefreshers, resourceKey)
		r.secretRefreshersMux.Unlock()
	}()

	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "secret refresher goroutine stopped", "resource", resourceKey)
			return
		case <-ticker.C:
			r.refreshImagePullSecret(ctx, locoRes)
		}
	}
}

// refreshImagePullSecret refreshes the image pull secret token if it's about to expire
func (r *LocoResourceReconciler) refreshImagePullSecret(ctx context.Context, locoRes *locov1alpha1.LocoResource) {
	namespace := getNamespace(locoRes)
	secretName := getImageSecretName(locoRes)

	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, secret); err != nil {
		slog.ErrorContext(ctx, "failed to fetch image pull secret for refresh", "namespace", namespace, "name", secretName, "error", err)
		return
	}

	expiryStr, ok := secret.Annotations["tokenExpiry"]
	if !ok {
		slog.WarnContext(ctx, "image pull secret missing tokenExpiry annotation", "namespace", namespace, "name", secretName)
		return
	}

	expiryTime, err := time.Parse(time.RFC3339, expiryStr)
	if err != nil {
		slog.ErrorContext(ctx, "failed to parse tokenExpiry annotation", "namespace", namespace, "name", secretName, "error", err)
		return
	}

	timeUntilExpiry := time.Until(expiryTime)
	if timeUntilExpiry > 10*time.Minute {
		slog.DebugContext(ctx, "token not yet expired, skipping refresh", "namespace", namespace, "name", secretName, "timeUntilExpiry", timeUntilExpiry)
		return
	}

	slog.InfoContext(ctx, "refreshing image pull secret token", "namespace", namespace, "name", secretName)

	token, err := r.getGitlabDeployToken(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get new gitlab deploy token", "namespace", namespace, "name", secretName, "error", err)
		return
	}

	dockerConfig, err := buildDockerConfig(r.gitlabRegistryURL, token.Username, token.Token)
	if err != nil {
		slog.ErrorContext(ctx, "failed to build docker config", "namespace", namespace, "name", secretName, "error", err)
		return
	}
	newExpiryTime := time.Now().Add(55 * time.Minute).UTC().Format(time.RFC3339)

	secret.Data[".dockerconfigjson"] = dockerConfig
	secret.Annotations["tokenExpiry"] = newExpiryTime

	err = r.Update(ctx, secret)
	if err != nil {
		slog.ErrorContext(ctx, "failed to update image pull secret", "namespace", namespace, "name", secretName, "error", err)
		return
	}

	slog.InfoContext(ctx, "image pull secret token refreshed successfully", "namespace", namespace, "name", secretName)
}

// getGitlabDeployToken fetches a new deploy token from GitLab
func (r *LocoResourceReconciler) getGitlabDeployToken(ctx context.Context) (*gitlabDeployTokenResponse, error) {
	expiresAt := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	payload := map[string]any{
		"name":       "loco-deploy-token",
		"scopes":     []string{"read_registry"},
		"expires_at": expiresAt,
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	deployTokenPath := fmt.Sprintf("%s/api/v4/projects/%s/deploy_tokens", r.gitlabURL, r.gitlabProjectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deployTokenPath, bytes.NewReader(payloadJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("PRIVATE-TOKEN", r.gitlabPAT)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.ErrorContext(ctx, "failed to execute gitlab api request", "error", err)
		return nil, fmt.Errorf("failed to create deploy token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.ErrorContext(ctx, "failed to read response body", "error", err)
			return nil, err
		}

		slog.ErrorContext(ctx, "unexpected status from gitlab api",
			"status_code", resp.StatusCode,
			"response", string(respBody),
		)
		return nil, fmt.Errorf("gitlab api returned status %d", resp.StatusCode)
	}

	var tokenResp gitlabDeployTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &tokenResp, nil
}

// gitlabDeployTokenResponse represents a GitLab deploy token response
type gitlabDeployTokenResponse struct {
	Username  string `json:"username"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// buildDockerConfig builds a .dockerconfigjson byte array for registry authentication
func buildDockerConfig(registryURL, username, token string) ([]byte, error) {
	authStr := base64.StdEncoding.EncodeToString([]byte(username + ":" + token))

	config := map[string]any{
		"auths": map[string]any{
			registryURL: map[string]string{
				"auth": authStr,
			},
		},
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal docker config: %w", err)
	}

	return configJSON, nil
}

// validateLocoResource validates that required fields exist in the spec
func (r *LocoResourceReconciler) validateLocoResource(locoRes *locov1alpha1.LocoResource) error {
	if locoRes.Spec.ServiceSpec == nil {
		return fmt.Errorf("ServiceSpec is required")
	}
	if locoRes.Spec.ServiceSpec.Deployment == nil {
		return fmt.Errorf("ServiceSpec.Deployment is required")
	}
	if locoRes.Spec.ServiceSpec.Deployment.Image == "" {
		return fmt.Errorf("Image is required")
	}
	if locoRes.Spec.ResourceId == 0 {
		return fmt.Errorf("ResourceId is required")
	}
	if locoRes.Spec.WorkspaceID == 0 {
		return fmt.Errorf("WorkspaceID is required")
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LocoResourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.secretRefreshers = make(map[string]context.CancelFunc)
	r.gitlabURL = os.Getenv("GITLAB_URL")
	r.gitlabPAT = os.Getenv("GITLAB_PAT")
	r.gitlabProjectID = os.Getenv("GITLAB_PROJECT_ID")
	r.gitlabRegistryURL = os.Getenv("GITLAB_REGISTRY_URL")

	if r.gitlabURL == "" || r.gitlabPAT == "" || r.gitlabProjectID == "" || r.gitlabRegistryURL == "" {
		slog.Error("missing required gitlab environment variables")
		return fmt.Errorf("missing required gitlab environment variables")
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&locov1alpha1.LocoResource{}).
		Named("locoresource").
		Complete(r)
}
