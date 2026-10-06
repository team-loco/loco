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
	"net/http"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"
	gatewayac "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// todo: finalize on the domain we wanna use inside kubernetes.
const (
	finalizerSecretRefresher = "loco.io/secret-refresher"
	labelApp                 = "app"
	labelWorkspaceID         = "loco.io/workspace-id"
	labelResourceID          = "loco.io/resource-id"
	labelEnvironmentID       = "loco.io/environment-id"
	labelManagedBy           = "app.kubernetes.io/managed-by"
	managedByValue           = "loco-controller"
	annotationAppNamespace   = "loco.io/application-namespace"
	annotationAppName        = "loco.io/application-name"
	annotationEnvSecretRV    = "loco.io/env-secret-version"
	fieldOwner               = "loco-controller"
	phaseDeploying           = "Deploying"
	phaseFailed              = "Failed"
	phaseReady               = "Ready"
	servicePort              = int32(80)
	defaultContainerPort     = int32(8080)
	defaultCPURequest        = "100m"
	defaultCPULimit          = "500m"
	defaultMemoryRequest     = "128Mi"
	defaultMemoryLimit       = "512Mi"
	deployingRequeue         = 15 * time.Second
	minRequeue               = time.Second
	maxConcurrentReconciles  = 4
)

// LocoResourceReconciler reconciles a Application object
type LocoResourceReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// needed for refreshing container image token.
	gitlabURL         string
	gitlabPAT         string
	gitlabProjectID   string
	gitlabRegistryURL string
	locoNamespace     string
	httpClient        *http.Client
}

// +kubebuilder:rbac:groups=infra.loco.io,resources=applications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infra.loco.io,resources=applications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infra.loco.io,resources=applications/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;create;list;watch;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;create;list;watch;patch;update;delete
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;create;list;watch;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;create;list;watch;patch;update;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;create;list;watch;patch;update;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;create;list;watch;patch;update;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;create;list;watch;patch;update;delete

// todo: abuse of power. we should delete based on owner refs, not delete namespace access;

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *LocoResourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	slog.DebugContext(ctx, "reconciling application", "namespace", req.Namespace, "name", req.Name)

	// fetch the Application
	locoRes := &locov1alpha1.Application{}
	if err := r.Get(ctx, req.NamespacedName, locoRes); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetch application: %w", err)
	}

	// deletion timestamp means we need to clean up
	if !locoRes.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, locoRes)
	}

	// validate early to prevent nil panics. Same validator the API runs before dispatching
	// a deploy, so this only fires for Applications written directly against the cluster.
	if err := locoRes.Spec.Validate(); err != nil {
		slog.ErrorContext(ctx, "invalid Application spec", "error", err)
		original := locoRes.DeepCopy()
		message := fmt.Sprintf("validation failed: %v", err)
		observePlacementRevision(locoRes)
		setPhase(locoRes, phaseFailed, message)
		if statusErr := r.patchStatus(ctx, locoRes, original); statusErr != nil {
			slog.ErrorContext(ctx, "failed to update status after validation error", "error", statusErr)
		}
		wrapped := fmt.Errorf("invalid application spec: %w", err)
		return ctrl.Result{}, reconcile.TerminalError(wrapped)
	}

	// ensure finalizer
	if err := r.ensureFinalizer(ctx, locoRes); err != nil {
		return ctrl.Result{}, err
	}

	original := locoRes.DeepCopy()
	observePlacementRevision(locoRes)
	result, err := r.reconcileResources(ctx, locoRes)
	if err != nil {
		slog.ErrorContext(ctx, "failed to reconcile application", "error", err)
		message := fmt.Sprintf("failed to %v", err)
		setPhase(locoRes, phaseFailed, message)
	}

	if statusErr := r.patchStatus(ctx, locoRes, original); statusErr != nil {
		slog.ErrorContext(ctx, "failed to update status", "error", statusErr)
		if err == nil {
			return ctrl.Result{}, statusErr
		}
	}

	slog.DebugContext(ctx, "reconcile complete", "phase", locoRes.Status.Phase, "resource", locoRes.Name)
	return result, err
}

func (r *LocoResourceReconciler) reconcileResources(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
) (ctrl.Result, error) {
	// begin reconcile steps - these functions allocate and ensure Kubernetes resources
	if err := ensureNamespace(ctx, r.Client, locoRes); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure namespace: %w", err)
	}

	envSecretVersion, err := ensureEnvSecret(ctx, r.Client, locoRes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure secrets: %w", err)
	}

	refreshAt, err := r.ensureImagePullSecret(ctx, locoRes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure image pull secret: %w", err)
	}

	err = r.ensureServiceAccount(ctx, locoRes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure service account: %w", err)
	}

	err = r.ensureRoleAndBinding(ctx, locoRes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure role & binding: %w", err)
	}

	dep, err := r.ensureDeployment(ctx, locoRes, envSecretVersion)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure deployment: %w", err)
	}

	err = r.ensureService(ctx, locoRes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure service: %w", err)
	}

	err = r.ensureHTTPRoute(ctx, locoRes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure HTTP route: %w", err)
	}

	// aggregate deployment status into our status
	replicas := desiredReplicas(locoRes.Spec.ServiceSpec)
	if !deploymentReady(dep, replicas) {
		setPhase(locoRes, phaseDeploying, "Waiting for pods to be ready...")
		requeue := requeueAfter(refreshAt, deployingRequeue)
		return ctrl.Result{RequeueAfter: requeue}, nil
	}

	locoRes.Status.DeployedGeneration = locoRes.Generation
	setPhase(locoRes, phaseReady, "Deployment ready")
	requeue := requeueAfter(refreshAt, 0)
	return ctrl.Result{RequeueAfter: requeue}, nil
}

func requeueAfter(refreshAt time.Time, ceiling time.Duration) time.Duration {
	wait := time.Until(refreshAt)
	if ceiling > 0 && wait > ceiling {
		wait = ceiling
	}
	if wait < minRequeue {
		wait = minRequeue
	}
	return wait
}

func observePlacementRevision(locoRes *locov1alpha1.Application) {
	revision, ok := locoRes.PlacementRevision()
	if !ok {
		return
	}
	locoRes.Status.ObservedPlacementRevision = revision
}

func setPhase(locoRes *locov1alpha1.Application, phase, message string) {
	if locoRes.Status.Phase == phase && locoRes.Status.Message == message {
		return
	}
	now := metav1.Now()
	if locoRes.Status.StartedAt == nil {
		locoRes.Status.StartedAt = &now
	}
	if phase == phaseReady && locoRes.Status.Phase != phaseReady {
		locoRes.Status.CompletedAt = &now
	}
	locoRes.Status.Phase = phase
	locoRes.Status.Message = message
	locoRes.Status.UpdatedAt = &now
}

func (r *LocoResourceReconciler) patchStatus(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
	original *locov1alpha1.Application,
) error {
	if equality.Semantic.DeepEqual(original.Status, locoRes.Status) {
		return nil
	}
	patch := client.MergeFrom(original)
	if err := r.Status().Patch(ctx, locoRes, patch); err != nil {
		return fmt.Errorf("patch application status: %w", err)
	}
	return nil
}

func (r *LocoResourceReconciler) ensureFinalizer(ctx context.Context, locoRes *locov1alpha1.Application) error {
	if controllerutil.ContainsFinalizer(locoRes, finalizerSecretRefresher) {
		return nil
	}
	original := locoRes.DeepCopy()
	controllerutil.AddFinalizer(locoRes, finalizerSecretRefresher)
	patch := client.MergeFrom(original)
	if err := r.Patch(ctx, locoRes, patch); err != nil {
		return fmt.Errorf("add finalizer: %w", err)
	}
	slog.DebugContext(ctx, "added finalizer", "finalizer", finalizerSecretRefresher)
	return nil
}

// handleDeletion revokes the registry token, deletes the namespace, and removes the finalizer
func (r *LocoResourceReconciler) handleDeletion(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(locoRes, finalizerSecretRefresher) {
		return ctrl.Result{}, nil
	}

	r.revokeCurrentRegistryToken(ctx, locoRes)

	if err := r.deleteAppObjects(ctx, locoRes); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.deleteNamespaceIfUnused(ctx, locoRes); err != nil {
		return ctrl.Result{}, err
	}

	original := locoRes.DeepCopy()
	controllerutil.RemoveFinalizer(locoRes, finalizerSecretRefresher)
	patch := client.MergeFrom(original)
	if err := r.Patch(ctx, locoRes, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	slog.InfoContext(ctx, "removed finalizer", "finalizer", finalizerSecretRefresher)

	return ctrl.Result{}, nil
}

func (r *LocoResourceReconciler) deleteAppObjects(ctx context.Context, locoRes *locov1alpha1.Application) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	routeName := name + "-route"
	bindingName := name + "-binding"
	roleName := name + "-role"
	envSecretName := getEnvSecretName(locoRes)
	imageSecretName := getImageSecretName(locoRes)

	objects := []client.Object{
		&v1Gateway.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: bindingName, Namespace: namespace}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: namespace}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: envSecretName, Namespace: namespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: imageSecretName, Namespace: namespace}},
	}
	for _, obj := range objects {
		err := r.Delete(ctx, obj)
		if err == nil || apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			continue
		}
		return fmt.Errorf("delete %T %s/%s: %w", obj, namespace, obj.GetName(), err)
	}
	slog.InfoContext(ctx, "app objects deleted", "namespace", namespace, "name", name)
	return nil
}

func (r *LocoResourceReconciler) deleteNamespaceIfUnused(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
) error {
	var apps locov1alpha1.ApplicationList
	if err := r.List(ctx, &apps, client.InNamespace(locoRes.Namespace)); err != nil {
		return fmt.Errorf("list applications: %w", err)
	}
	for i := range apps.Items {
		other := &apps.Items[i]
		if other.Name == locoRes.Name || other.DeletionTimestamp != nil {
			continue
		}
		if other.Spec.WorkspaceID == locoRes.Spec.WorkspaceID {
			return nil
		}
	}

	namespace := getNamespace(locoRes)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	if err := r.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete namespace %s: %w", namespace, err)
	}
	slog.InfoContext(ctx, "namespace deleted", "namespace", namespace)
	return nil
}

// getName derives the app name from the Application
func getName(locoRes *locov1alpha1.Application) string {
	return fmt.Sprintf("resource-%v", locoRes.Spec.ResourceID)
}

// getNamespace derives the namespace from the Application
func getNamespace(locoRes *locov1alpha1.Application) string {
	return fmt.Sprintf("ws-%v", locoRes.Spec.WorkspaceID)
}

func getImageSecretName(locoRes *locov1alpha1.Application) string {
	return fmt.Sprintf("%s-image-pull", getName(locoRes))
}

func getEnvSecretName(locoRes *locov1alpha1.Application) string {
	return fmt.Sprintf("%s-env", getName(locoRes))
}

func getInternalDomain(locoRes *locov1alpha1.Application) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", getName(locoRes), getNamespace(locoRes))
}

func managedLabels(locoRes *locov1alpha1.Application) map[string]string {
	name := getName(locoRes)
	return map[string]string{
		labelApp:       name,
		labelManagedBy: managedByValue,
	}
}

func ownerAnnotations(locoRes *locov1alpha1.Application) map[string]string {
	return map[string]string{
		annotationAppNamespace: locoRes.Namespace,
		annotationAppName:      locoRes.Name,
	}
}

func applyOptions() []client.ApplyOption {
	return []client.ApplyOption{client.FieldOwner(fieldOwner), client.ForceOwnership}
}

// ensureNamespace ensures the application namespace exists and is configured
func ensureNamespace(ctx context.Context, kubeClient client.Client, locoRes *locov1alpha1.Application) error {
	namespace := getNamespace(locoRes)
	slog.DebugContext(ctx, "ensuring namespace", "namespace", namespace)

	labels := map[string]string{
		"loco.io/app":    "true",
		labelManagedBy:   managedByValue,
		labelWorkspaceID: locoRes.Spec.WorkspaceID,
	}
	ns := corev1ac.Namespace(namespace).WithLabels(labels)

	opts := applyOptions()
	if err := kubeClient.Apply(ctx, ns, opts...); err != nil {
		return fmt.Errorf("apply namespace %s: %w", namespace, err)
	}
	return nil
}

// ensureEnvSecret ensures all required secrets exist in the app namespace
func ensureEnvSecret(
	ctx context.Context,
	kubeClient client.Client,
	locoRes *locov1alpha1.Application,
) (string, error) {
	namespace := getNamespace(locoRes)
	envSecretName := getEnvSecretName(locoRes)
	slog.DebugContext(ctx, "ensuring env secret", "namespace", namespace, "name", envSecretName)

	env := locoRes.Spec.ServiceSpec.Deployment.Env
	secretData := make(map[string][]byte, len(env))
	for k, v := range env {
		secretData[k] = []byte(v)
	}

	labels := managedLabels(locoRes)
	annotations := ownerAnnotations(locoRes)
	envSecret := corev1ac.Secret(envSecretName, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithType(corev1.SecretTypeOpaque).
		WithData(secretData)

	opts := applyOptions()
	if err := kubeClient.Apply(ctx, envSecret, opts...); err != nil {
		return "", fmt.Errorf("apply env secret %s/%s: %w", namespace, envSecretName, err)
	}

	return ptr.Deref(envSecret.ResourceVersion, ""), nil
}

// ensureServiceAccount ensures the service account exists for the deployment and references image pull secret
func (r *LocoResourceReconciler) ensureServiceAccount(ctx context.Context, locoRes *locov1alpha1.Application) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	secretName := getImageSecretName(locoRes)

	slog.DebugContext(ctx, "ensuring service account", "namespace", namespace, "name", name)

	labels := managedLabels(locoRes)
	annotations := ownerAnnotations(locoRes)
	pullSecret := corev1ac.LocalObjectReference().WithName(secretName)
	sa := corev1ac.ServiceAccount(name, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithImagePullSecrets(pullSecret)

	opts := applyOptions()
	if err := r.Apply(ctx, sa, opts...); err != nil {
		return fmt.Errorf("apply service account %s/%s: %w", namespace, name, err)
	}
	return nil
}

// ensureRoleAndBinding ensures the RBAC role and role binding exist
func (r *LocoResourceReconciler) ensureRoleAndBinding(ctx context.Context, locoRes *locov1alpha1.Application) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	slog.DebugContext(ctx, "ensuring role and role binding", "namespace", namespace, "name", name)

	envSecretName := getEnvSecretName(locoRes)
	roleName := fmt.Sprintf("%s-role", name)
	roleBindingName := fmt.Sprintf("%s-binding", name)
	labels := managedLabels(locoRes)
	annotations := ownerAnnotations(locoRes)
	opts := applyOptions()

	rule := rbacv1ac.PolicyRule().
		WithAPIGroups("").
		WithResources("secrets").
		WithVerbs("get", "list", "watch").
		WithResourceNames(envSecretName)
	role := rbacv1ac.Role(roleName, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithRules(rule)
	if err := r.Apply(ctx, role, opts...); err != nil {
		return fmt.Errorf("apply role %s/%s: %w", namespace, roleName, err)
	}

	subject := rbacv1ac.Subject().
		WithKind(rbacv1.ServiceAccountKind).
		WithName(name).
		WithNamespace(namespace)
	roleRef := rbacv1ac.RoleRef().
		WithKind("Role").
		WithName(roleName).
		WithAPIGroup(rbacv1.GroupName)
	binding := rbacv1ac.RoleBinding(roleBindingName, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithSubjects(subject).
		WithRoleRef(roleRef)
	if err := r.Apply(ctx, binding, opts...); err != nil {
		return fmt.Errorf("apply role binding %s/%s: %w", namespace, roleBindingName, err)
	}

	return nil
}

// ensureService ensures the Kubernetes service exists for the deployment
func (r *LocoResourceReconciler) ensureService(ctx context.Context, locoRes *locov1alpha1.Application) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	containerPort := appPort(locoRes)

	slog.DebugContext(ctx, "ensuring service", "namespace", namespace, "name", name, "containerPort", containerPort)

	labels := managedLabels(locoRes)
	annotations := ownerAnnotations(locoRes)
	selector := map[string]string{labelApp: name}
	targetPort := intstr.FromInt32(containerPort)
	port := corev1ac.ServicePort().
		WithName("http").
		WithProtocol(corev1.ProtocolTCP).
		WithPort(servicePort).
		WithTargetPort(targetPort)
	spec := corev1ac.ServiceSpec().
		WithType(corev1.ServiceTypeClusterIP).
		WithSelector(selector).
		WithPorts(port)
	svc := corev1ac.Service(name, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithSpec(spec)

	opts := applyOptions()
	if err := r.Apply(ctx, svc, opts...); err != nil {
		return fmt.Errorf("apply service %s/%s: %w", namespace, name, err)
	}
	return nil
}

func appPort(locoRes *locov1alpha1.Application) int32 {
	if locoRes.Spec.ServiceSpec.Deployment.Port > 0 {
		return locoRes.Spec.ServiceSpec.Deployment.Port
	}
	return defaultContainerPort
}

func desiredReplicas(spec *locov1alpha1.ServiceSpec) int32 {
	if spec.Resources != nil && spec.Resources.Replicas.Min > 0 {
		return spec.Resources.Replicas.Min
	}
	if spec.Deployment != nil && spec.Deployment.MinReplicas > 0 {
		return spec.Deployment.MinReplicas
	}
	return 1
}

func containerResources(spec *locov1alpha1.ServiceSpec) (*corev1ac.ResourceRequirementsApplyConfiguration, error) {
	cpuRequest := defaultCPURequest
	cpuLimit := defaultCPULimit
	memoryRequest := defaultMemoryRequest
	memoryLimit := defaultMemoryLimit
	if spec.Resources != nil && spec.Resources.CPU != "" {
		cpuRequest = spec.Resources.CPU
		cpuLimit = spec.Resources.CPU
	}
	if spec.Resources != nil && spec.Resources.Memory != "" {
		memoryRequest = spec.Resources.Memory
		memoryLimit = spec.Resources.Memory
	}

	requests, err := parseResourceList(cpuRequest, memoryRequest)
	if err != nil {
		return nil, fmt.Errorf("requests: %w", err)
	}
	limits, err := parseResourceList(cpuLimit, memoryLimit)
	if err != nil {
		return nil, fmt.Errorf("limits: %w", err)
	}
	return corev1ac.ResourceRequirements().WithRequests(requests).WithLimits(limits), nil
}

func parseResourceList(cpu, memory string) (corev1.ResourceList, error) {
	cpuQuantity, err := resource.ParseQuantity(cpu)
	if err != nil {
		return nil, fmt.Errorf("parse cpu %q: %w", cpu, err)
	}
	memoryQuantity, err := resource.ParseQuantity(memory)
	if err != nil {
		return nil, fmt.Errorf("parse memory %q: %w", memory, err)
	}
	return corev1.ResourceList{
		corev1.ResourceCPU:    cpuQuantity,
		corev1.ResourceMemory: memoryQuantity,
	}, nil
}

func systemEnvVars(locoRes *locov1alpha1.Application) []*corev1ac.EnvVarApplyConfiguration {
	publicDomain := ""
	if locoRes.Spec.ServiceSpec.Routing != nil {
		publicDomain = locoRes.Spec.ServiceSpec.Routing.HostName
	}
	internalDomain := getInternalDomain(locoRes)

	vars := []corev1.EnvVar{
		{Name: "LOCO_APP_NAME", Value: locoRes.Name},
		{Name: "LOCO_RESOURCE_ID", Value: locoRes.Spec.ResourceID},
		{Name: "LOCO_WORKSPACE_ID", Value: locoRes.Spec.WorkspaceID},
		{Name: "LOCO_DEPLOYMENT_ID", Value: locoRes.Spec.DeploymentID},
		{Name: "LOCO_REGION", Value: locoRes.Spec.Region},
		{Name: "LOCO_ENVIRONMENT", Value: locoRes.Spec.EnvironmentName},
		{Name: "LOCO_INTERNAL_DOMAIN", Value: internalDomain},
		{Name: "LOCO_PUBLIC_DOMAIN", Value: publicDomain},
	}

	envVars := make([]*corev1ac.EnvVarApplyConfiguration, 0, len(vars))
	for _, v := range vars {
		envVar := corev1ac.EnvVar().WithName(v.Name).WithValue(v.Value)
		envVars = append(envVars, envVar)
	}
	return envVars
}

func healthProbe(hc *locov1alpha1.HealthCheckSpec, port int32) *corev1ac.ProbeApplyConfiguration {
	probePort := intstr.FromInt32(port)
	httpGet := corev1ac.HTTPGetAction().WithPath(hc.Path).WithPort(probePort)
	probe := corev1ac.Probe().WithHTTPGet(httpGet)
	if hc.StartupGracePeriod > 0 {
		probe.WithInitialDelaySeconds(hc.StartupGracePeriod)
	}
	if hc.Timeout > 0 {
		probe.WithTimeoutSeconds(hc.Timeout)
	}
	if hc.Interval > 0 {
		probe.WithPeriodSeconds(hc.Interval)
	}
	if hc.FailThreshold > 0 {
		probe.WithFailureThreshold(hc.FailThreshold)
	}
	return probe
}

func desiredDeployment(
	locoRes *locov1alpha1.Application,
	envSecretVersion string,
) (*appsv1ac.DeploymentApplyConfiguration, error) {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	spec := locoRes.Spec.ServiceSpec
	containerPort := appPort(locoRes)
	replicas := desiredReplicas(spec)

	resources, err := containerResources(spec)
	if err != nil {
		return nil, fmt.Errorf("resources: %w", err)
	}

	envSecretName := getEnvSecretName(locoRes)
	secretRef := corev1ac.SecretEnvSource().WithName(envSecretName)
	envFrom := corev1ac.EnvFromSource().WithSecretRef(secretRef)
	envVars := systemEnvVars(locoRes)
	port := corev1ac.ContainerPort().
		WithName("http").
		WithContainerPort(containerPort).
		WithProtocol(corev1.ProtocolTCP)
	container := corev1ac.Container().
		WithName(name).
		WithImage(spec.Deployment.Image).
		WithEnvFrom(envFrom).
		WithEnv(envVars...).
		WithPorts(port).
		WithResources(resources)

	if hc := spec.Deployment.HealthCheck; hc != nil {
		livenessProbe := healthProbe(hc, containerPort)
		readinessProbe := healthProbe(hc, containerPort)
		container.WithLivenessProbe(livenessProbe).WithReadinessProbe(readinessProbe)
	}

	podLabels := map[string]string{
		labelApp:           name,
		labelWorkspaceID:   locoRes.Spec.WorkspaceID,
		labelResourceID:    locoRes.Spec.ResourceID,
		labelEnvironmentID: locoRes.Spec.EnvironmentID,
	}
	podAnnotations := map[string]string{annotationEnvSecretRV: envSecretVersion}
	podSpec := corev1ac.PodSpec().
		WithServiceAccountName(name).
		WithRestartPolicy(corev1.RestartPolicyAlways).
		WithContainers(container)
	template := corev1ac.PodTemplateSpec().
		WithLabels(podLabels).
		WithAnnotations(podAnnotations).
		WithSpec(podSpec)

	selectorLabels := map[string]string{labelApp: name}
	selector := metav1ac.LabelSelector().WithMatchLabels(selectorLabels)
	surge := intstr.FromString("25%")
	rollingUpdate := appsv1ac.RollingUpdateDeployment().WithMaxSurge(surge).WithMaxUnavailable(surge)
	strategy := appsv1ac.DeploymentStrategy().
		WithType(appsv1.RollingUpdateDeploymentStrategyType).
		WithRollingUpdate(rollingUpdate)
	deploymentSpec := appsv1ac.DeploymentSpec().
		WithReplicas(replicas).
		WithSelector(selector).
		WithStrategy(strategy).
		WithTemplate(template)

	depLabels := managedLabels(locoRes)
	depLabels[labelWorkspaceID] = locoRes.Spec.WorkspaceID
	depLabels[labelResourceID] = locoRes.Spec.ResourceID
	depLabels[labelEnvironmentID] = locoRes.Spec.EnvironmentID
	annotations := ownerAnnotations(locoRes)

	return appsv1ac.Deployment(name, namespace).
		WithLabels(depLabels).
		WithAnnotations(annotations).
		WithSpec(deploymentSpec), nil
}

// ensureDeployment ensures the Kubernetes deployment exists and is configured with the spec
// Returns the applied deployment as reported by the API server
func (r *LocoResourceReconciler) ensureDeployment(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
	envSecretVersion string,
) (*appsv1ac.DeploymentApplyConfiguration, error) {
	dep, err := desiredDeployment(locoRes, envSecretVersion)
	if err != nil {
		return nil, err
	}

	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	slog.DebugContext(ctx, "ensuring deployment", "namespace", namespace, "name", name)

	opts := applyOptions()
	if err := r.Apply(ctx, dep, opts...); err != nil {
		return nil, fmt.Errorf("apply deployment %s/%s: %w", namespace, name, err)
	}
	return dep, nil
}

func deploymentReady(dep *appsv1ac.DeploymentApplyConfiguration, replicas int32) bool {
	if dep == nil || dep.ObjectMetaApplyConfiguration == nil || dep.Status == nil {
		return false
	}
	generation := ptr.Deref(dep.Generation, 0)
	observed := ptr.Deref(dep.Status.ObservedGeneration, 0)
	updated := ptr.Deref(dep.Status.UpdatedReplicas, 0)
	available := ptr.Deref(dep.Status.AvailableReplicas, 0)
	return generation > 0 && observed == generation && updated == replicas && available == replicas
}

// ensureHTTPRoute ensures the HTTPRoute exists for traffic ingress (Envoy Gateway)
func (r *LocoResourceReconciler) ensureHTTPRoute(ctx context.Context, locoRes *locov1alpha1.Application) error {
	name := getName(locoRes)
	namespace := getNamespace(locoRes)
	routeName := fmt.Sprintf("%s-route", name)
	routing := locoRes.Spec.ServiceSpec.Routing

	if routing == nil {
		route := &v1Gateway.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: namespace}}
		err := r.Delete(ctx, route)
		if err != nil && !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
			return fmt.Errorf("delete HTTPRoute %s/%s: %w", namespace, routeName, err)
		}
		return nil
	}

	slog.DebugContext(ctx, "ensuring HTTPRoute", "namespace", namespace, "name", routeName)

	pathValue := "/"
	if routing.PathPrefix != "" {
		pathValue = routing.PathPrefix
	}

	// todo: remove the hardooded gateway name and namespace.
	gatewayNamespace := v1Gateway.Namespace(r.locoNamespace)
	parentRef := gatewayac.ParentReference().
		WithName("eg").
		WithNamespace(gatewayNamespace)
	pathMatch := gatewayac.HTTPPathMatch().
		WithType(v1Gateway.PathMatchPathPrefix).
		WithValue(pathValue)
	match := gatewayac.HTTPRouteMatch().WithPath(pathMatch)
	backendRef := gatewayac.HTTPBackendRef().
		WithKind("Service").
		WithName(v1Gateway.ObjectName(name)).
		WithPort(servicePort)
	rule := gatewayac.HTTPRouteRule().
		WithMatches(match).
		WithBackendRefs(backendRef)
	spec := gatewayac.HTTPRouteSpec().
		WithHostnames(v1Gateway.Hostname(routing.HostName)).
		WithParentRefs(parentRef).
		WithRules(rule)

	labels := managedLabels(locoRes)
	annotations := ownerAnnotations(locoRes)
	route := gatewayac.HTTPRoute(routeName, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithSpec(spec)

	opts := applyOptions()
	if err := r.Apply(ctx, route, opts...); err != nil {
		return fmt.Errorf("apply HTTPRoute %s/%s: %w", namespace, routeName, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LocoResourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.gitlabURL = os.Getenv("GITLAB_URL")
	r.gitlabPAT = os.Getenv("GITLAB_PAT")
	r.gitlabProjectID = os.Getenv("GITLAB_PROJECT_ID")
	r.gitlabRegistryURL = os.Getenv("GITLAB_REGISTRY_URL")
	r.locoNamespace = os.Getenv("LOCO_NAMESPACE")
	r.httpClient = &http.Client{Timeout: 10 * time.Second}

	if r.gitlabURL == "" || r.gitlabPAT == "" || r.gitlabProjectID == "" || r.gitlabRegistryURL == "" {
		slog.Error("missing required gitlab environment variables")
		return fmt.Errorf("missing required gitlab environment variables")
	}

	applicationChanged := predicate.Or[client.Object](
		predicate.GenerationChangedPredicate{},
		predicate.AnnotationChangedPredicate{},
	)
	applicationPredicates := builder.WithPredicates(applicationChanged)
	deploymentHandler := handler.EnqueueRequestsFromMapFunc(applicationForObject)
	options := crcontroller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}

	return ctrl.NewControllerManagedBy(mgr).
		For(&locov1alpha1.Application{}, applicationPredicates).
		Watches(&appsv1.Deployment{}, deploymentHandler).
		WithOptions(options).
		Named("application").
		Complete(r)
}
