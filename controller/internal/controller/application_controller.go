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
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
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

	"github.com/team-loco/loco/controller/internal/isolation"
	"github.com/team-loco/loco/controller/internal/managed"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	labelApp                = "app"
	envRegion               = "LOCO_REGION"
	componentApplication    = "application"
	servicePort             = int32(80)
	deployingRequeue        = 15 * time.Second
	envSecretRequeue        = 5 * time.Second
	maxConcurrentReconciles = 4
)

var (
	errEnvSecretPending   = errors.New("the staging env secret does not hold the referenced revision")
	errEnvSecretPlacement = errors.New("the staging env secret belongs to another placement")
)

// ApplicationReconciler reconciles an Application object
type ApplicationReconciler struct {
	RuntimeRecoveryWindow time.Duration
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme

	LocoNamespace            string
	PullSecretName           string
	RegistryTokenMode        bool
	Gateway                  string
	WorkspaceNamespacePrefix string
	Telemetry                TelemetryConfig
}

// +kubebuilder:rbac:groups=infra.loco.io,resources=applications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infra.loco.io,resources=applications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infra.loco.io,resources=applications/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;create;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;create;list;watch;patch;update;delete
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;create;list;watch;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;create;list;watch;patch;update;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;create;list;watch;patch;update;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;create;list;watch;patch;update;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;create;list;watch;patch;update;delete
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=servicecidrs,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ApplicationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	slog.DebugContext(ctx, "reconciling application", "namespace", req.Namespace, "name", req.Name)

	app := &locov1alpha1.Application{}
	if err := r.Get(ctx, req.NamespacedName, app); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetch application: %w", err)
	}

	if !app.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, app)
	}

	// validate early to prevent nil panics. Same validator the API runs before dispatching
	// a deploy, so this only fires for Applications written directly against the cluster.
	if err := app.Spec.Validate(); err != nil {
		original := app.DeepCopy()
		message := fmt.Sprintf("validation failed: %v", err)
		observePlacementRevision(app)
		setPhase(app, locov1alpha1.ApplicationPhaseFailed, message)
		wrapped := fmt.Errorf("invalid application spec: %w", err)
		statusErr := r.patchStatus(ctx, app, original)
		joined := errors.Join(wrapped, statusErr)
		return ctrl.Result{}, reconcile.TerminalError(joined)
	}

	if err := r.ensureFinalizer(ctx, app); err != nil {
		return ctrl.Result{}, err
	}

	original := app.DeepCopy()
	observePlacementRevision(app)
	result, err := r.reconcileResources(ctx, app)
	if err != nil {
		message := fmt.Sprintf("failed to %v", err)
		setPhase(app, locov1alpha1.ApplicationPhaseFailed, message)
	}

	if statusErr := r.patchStatus(ctx, app, original); statusErr != nil {
		return ctrl.Result{}, errors.Join(err, statusErr)
	}

	slog.DebugContext(ctx, "reconcile complete", "phase", app.Status.Phase, "resource", app.Name)
	return result, err
}

func (r *ApplicationReconciler) reconcileResources(
	ctx context.Context,
	app *locov1alpha1.Application,
) (ctrl.Result, error) {
	if err := r.ensureNamespace(ctx, app); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure namespace: %w", err)
	}

	if err := r.ensureWorkspaceNetworkPolicies(ctx, app); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure workspace network policies: %w", err)
	}

	err := r.ensureWorkspacePullSecret(ctx, app)
	if errors.Is(err, errRegistryPullSecretPending) {
		slog.DebugContext(ctx, "waiting for the registry pull secret", "error", err)
		setPhase(app, locov1alpha1.ApplicationPhaseDeploying, messageRegistryPullSecretWait)
		return ctrl.Result{RequeueAfter: registryPullSecretRequeue}, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure workspace pull secret: %w", err)
	}

	envSecret, err := r.ensureEnvSecret(ctx, app)
	if errors.Is(err, errEnvSecretPending) {
		setPhase(app, locov1alpha1.ApplicationPhaseDeploying, "Waiting for the environment secrets...")
		return ctrl.Result{RequeueAfter: envSecretRequeue}, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure secrets: %w", err)
	}

	err = r.ensureServiceAccount(ctx, app)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure service account: %w", err)
	}

	dep, err := r.ensureDeployment(ctx, app, envSecret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure deployment: %w", err)
	}
	setReplicaCounts(app, dep)
	if runtimeErr := r.observeRuntimeProblem(
		ctx,
		app,
		dep.Spec.Template.Annotations[locolabels.AnnotationEnvSecretRevision],
	); runtimeErr != nil {
		return ctrl.Result{}, runtimeErr
	}

	err = r.ensureService(ctx, app)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure service: %w", err)
	}

	err = r.ensurePrivateService(ctx, app)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure private service: %w", err)
	}

	err = r.ensureMetricsIngressPolicy(ctx, app)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure metrics ingress policy: %w", err)
	}
	err = r.ensureGatewayIngressPolicy(ctx, app)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure gateway ingress policy: %w", err)
	}

	err = r.ensureHTTPRoute(ctx, app)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("ensure HTTP route: %w", err)
	}

	replicas := app.Spec.ServiceSpec.Resources.Replicas.Min
	if !deploymentReady(dep, replicas) {
		if failure, found := deploymentFailure(dep); found {
			setDegraded(app, failure)
			setPhase(app, locov1alpha1.ApplicationPhaseFailed, failure.message)
			return ctrl.Result{RequeueAfter: deployingRequeue}, nil
		}
		envSecretRevision := dep.Spec.Template.Annotations[locolabels.AnnotationEnvSecretRevision]
		stuck, found, err := r.findStuckReplica(ctx, app, envSecretRevision)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("find stuck replicas: %w", err)
		}
		if found {
			setDegraded(app, stuck)
			setPhase(app, locov1alpha1.ApplicationPhaseFailed, stuck.message)
			return ctrl.Result{RequeueAfter: deployingRequeue}, nil
		}
		clearDegraded(app)
		setPhase(app, locov1alpha1.ApplicationPhaseDeploying, "Waiting for replicas to become ready")
		return ctrl.Result{RequeueAfter: deployingRequeue}, nil
	}

	clearDegraded(app)
	app.Status.DeployedGeneration = app.Generation
	setPhase(app, locov1alpha1.ApplicationPhaseReady, "Deployment ready")
	if app.Status.RuntimeReason != "" {
		return ctrl.Result{RequeueAfter: deployingRequeue}, nil
	}
	return ctrl.Result{}, nil
}

func observePlacementRevision(app *locov1alpha1.Application) {
	revision, ok := app.PlacementRevision()
	if !ok {
		return
	}
	app.Status.ObservedPlacementRevision = revision
}

func setPhase(app *locov1alpha1.Application, phase, message string) {
	if app.Status.Phase == phase && app.Status.Message == message {
		return
	}
	now := metav1.Now()
	if app.Status.StartedAt == nil {
		app.Status.StartedAt = &now
	}
	if phase == locov1alpha1.ApplicationPhaseReady && app.Status.Phase != locov1alpha1.ApplicationPhaseReady {
		app.Status.CompletedAt = &now
	}
	app.Status.Phase = phase
	app.Status.Message = message
	app.Status.UpdatedAt = &now
}

func (r *ApplicationReconciler) patchStatus(
	ctx context.Context,
	app *locov1alpha1.Application,
	original *locov1alpha1.Application,
) error {
	if equality.Semantic.DeepEqual(original.Status, app.Status) {
		return nil
	}
	// A cached copy can predate our own last status write; the lock makes that patch conflict instead of restamping it.
	patch := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
	if err := r.Status().Patch(ctx, app, patch); err != nil {
		return fmt.Errorf("patch application status: %w", err)
	}
	return nil
}

func (r *ApplicationReconciler) ensureFinalizer(ctx context.Context, app *locov1alpha1.Application) error {
	if controllerutil.ContainsFinalizer(app, locov1alpha1.FinalizerAppResourcesCleanup) {
		return nil
	}
	original := app.DeepCopy()
	controllerutil.AddFinalizer(app, locov1alpha1.FinalizerAppResourcesCleanup)
	patch := client.MergeFrom(original)
	if err := r.Patch(ctx, app, patch); err != nil {
		return fmt.Errorf("add finalizer: %w", err)
	}
	slog.DebugContext(ctx, "added finalizer", "finalizer", locov1alpha1.FinalizerAppResourcesCleanup)
	return nil
}

func (r *ApplicationReconciler) handleDeletion(
	ctx context.Context,
	app *locov1alpha1.Application,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(app, locov1alpha1.FinalizerAppResourcesCleanup) {
		return ctrl.Result{}, nil
	}

	if err := r.deleteAppObjects(ctx, app); err != nil {
		return ctrl.Result{}, err
	}

	original := app.DeepCopy()
	controllerutil.RemoveFinalizer(app, locov1alpha1.FinalizerAppResourcesCleanup)
	patch := client.MergeFrom(original)
	if err := r.Patch(ctx, app, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	slog.InfoContext(ctx, "removed finalizer", "finalizer", locov1alpha1.FinalizerAppResourcesCleanup)

	return ctrl.Result{}, nil
}

func (r *ApplicationReconciler) deleteAppObjects(ctx context.Context, app *locov1alpha1.Application) error {
	if err := r.prunePrivateServices(ctx, app, ""); err != nil {
		return err
	}
	name := locov1alpha1.ApplicationName(app.Spec.ResourceID)
	namespace := r.workspaceNamespace(app)
	routeName := getRouteName(app)
	gatewayPolicyName := getGatewayPolicyName(app)
	envSecretName := getEnvSecretName(app)

	objects := []client.Object{
		&v1Gateway.HTTPRoute{Name: routeName, Namespace: namespace},
		&networkingv1.NetworkPolicy{Name: gatewayPolicyName, Namespace: namespace},
		&networkingv1.NetworkPolicy{Name: getMetricsPolicyName(app), Namespace: namespace},
		&corev1.Service{Name: name, Namespace: namespace},
		&appsv1.Deployment{Name: name, Namespace: namespace},
		&corev1.ServiceAccount{Name: name, Namespace: namespace},
		&corev1.Secret{Name: envSecretName, Namespace: namespace},
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

func (r *ApplicationReconciler) workspaceNamespace(app *locov1alpha1.Application) string {
	return r.WorkspaceNamespacePrefix + app.Spec.WorkspaceID
}

func getEnvSecretName(app *locov1alpha1.Application) string {
	return fmt.Sprintf("%s-env", locov1alpha1.ApplicationName(app.Spec.ResourceID))
}

func getRouteName(app *locov1alpha1.Application) string {
	return fmt.Sprintf("%s-route", locov1alpha1.ApplicationName(app.Spec.ResourceID))
}

func getGatewayPolicyName(app *locov1alpha1.Application) string {
	return fmt.Sprintf("%s-gateway", locov1alpha1.ApplicationName(app.Spec.ResourceID))
}

func getInternalDomain(app *locov1alpha1.Application, namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", locov1alpha1.ApplicationName(app.Spec.ResourceID), namespace)
}

func managedLabels(app *locov1alpha1.Application) map[string]string {
	name := locov1alpha1.ApplicationName(app.Spec.ResourceID)
	return map[string]string{
		labelApp:                  name,
		locolabels.LabelManagedBy: locolabels.ControllerManagedBy,
	}
}

func workloadLabels(app *locov1alpha1.Application) map[string]string {
	labels := managedLabels(app)
	labels[locolabels.LabelOrganizationID] = app.Spec.OrganizationID
	labels[locolabels.LabelWorkspaceID] = app.Spec.WorkspaceID
	labels[locolabels.LabelResourceID] = app.Spec.ResourceID
	labels[locolabels.LabelEnvironmentID] = app.Spec.EnvironmentID
	return labels
}

func ownerAnnotations(app *locov1alpha1.Application) map[string]string {
	return map[string]string{
		locolabels.AnnotationApplicationNamespace: app.Namespace,
		locolabels.AnnotationApplicationName:      app.Name,
	}
}

// ensureNamespace ensures the application namespace exists and is configured
func (r *ApplicationReconciler) ensureNamespace(ctx context.Context, app *locov1alpha1.Application) error {
	namespace := r.workspaceNamespace(app)
	slog.DebugContext(ctx, "ensuring namespace", "namespace", namespace)

	labels := workspaceNamespaceLabels(app)
	ns := corev1ac.Namespace(namespace).WithLabels(labels)

	opts := managed.ApplyOptions()
	if err := r.Apply(ctx, ns, opts...); err != nil {
		return fmt.Errorf("apply namespace %s: %w", namespace, err)
	}
	return nil
}

type envSecretMount struct {
	Mounted  bool
	Revision int64
	Names    []string
}

// ensureEnvSecret copies the staging Secret the Application references into the app namespace.
func (r *ApplicationReconciler) ensureEnvSecret(
	ctx context.Context,
	app *locov1alpha1.Application,
) (envSecretMount, error) {
	namespace := r.workspaceNamespace(app)
	envSecretName := getEnvSecretName(app)
	ref := app.Spec.ServiceSpec.Deployment.EnvSecretRef
	if ref == nil {
		stale := &corev1.Secret{Name: envSecretName, Namespace: namespace}
		if err := r.Delete(ctx, stale); err != nil && !apierrors.IsNotFound(err) {
			return envSecretMount{}, fmt.Errorf("delete env secret %s/%s: %w", namespace, envSecretName, err)
		}
		return envSecretMount{}, nil
	}
	slog.DebugContext(ctx, "ensuring env secret", "namespace", namespace, "name", envSecretName)

	staged := &corev1.Secret{}
	stagedKey := client.ObjectKey{Namespace: app.Namespace, Name: ref.Name}
	err := r.APIReader.Get(ctx, stagedKey, staged)
	if apierrors.IsNotFound(err) {
		return envSecretMount{}, errEnvSecretPending
	}
	if err != nil {
		return envSecretMount{}, fmt.Errorf("get staged env secret %s: %w", stagedKey, err)
	}
	placementID := app.PlacementID()
	if placementID == "" || staged.Labels[locov1alpha1.KeyPlacementID] != placementID {
		return envSecretMount{}, fmt.Errorf(
			"%w: %s, application placement %q", errEnvSecretPlacement, stagedKey, placementID)
	}
	revision, ok := locov1alpha1.EnvSecretRevision(staged.Labels)
	if !ok || revision != ref.Revision {
		return envSecretMount{}, errEnvSecretPending
	}

	labels := managedLabels(app)
	annotations := ownerAnnotations(app)
	envSecret := corev1ac.Secret(envSecretName, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithType(corev1.SecretTypeOpaque).
		WithData(staged.Data)

	opts := managed.ApplyOptions()
	if err := r.Apply(ctx, envSecret, opts...); err != nil {
		return envSecretMount{}, fmt.Errorf("apply env secret %s/%s: %w", namespace, envSecretName, err)
	}
	names := slices.Sorted(maps.Keys(staged.Data))
	return envSecretMount{Mounted: true, Revision: ref.Revision, Names: names}, nil
}

// ensureServiceAccount ensures the service account exists for the deployment and references image pull secret
func (r *ApplicationReconciler) ensureServiceAccount(ctx context.Context, app *locov1alpha1.Application) error {
	name := locov1alpha1.ApplicationName(app.Spec.ResourceID)
	namespace := r.workspaceNamespace(app)

	slog.DebugContext(ctx, "ensuring service account", "namespace", namespace, "name", name)

	labels := managedLabels(app)
	annotations := ownerAnnotations(app)
	sa := corev1ac.ServiceAccount(name, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithAutomountServiceAccountToken(false)
	if r.copiesPullSecret() {
		pullSecret := corev1ac.LocalObjectReference().WithName(workspacePullSecretName)
		sa.WithImagePullSecrets(pullSecret)
	}

	opts := managed.ApplyOptions()
	if err := r.Apply(ctx, sa, opts...); err != nil {
		return fmt.Errorf("apply service account %s/%s: %w", namespace, name, err)
	}
	return nil
}

// ensureService ensures the Kubernetes service exists for the deployment
func (r *ApplicationReconciler) ensureService(ctx context.Context, app *locov1alpha1.Application) error {
	name := locov1alpha1.ApplicationName(app.Spec.ResourceID)
	namespace := r.workspaceNamespace(app)
	containerPort := app.Spec.ServiceSpec.Deployment.Port

	slog.DebugContext(ctx, "ensuring service", "namespace", namespace, "name", name, "containerPort", containerPort)

	labels := managedLabels(app)
	annotations := ownerAnnotations(app)
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

	opts := managed.ApplyOptions()
	if err := r.Apply(ctx, svc, opts...); err != nil {
		return fmt.Errorf("apply service %s/%s: %w", namespace, name, err)
	}
	return nil
}

func containerResources(
	resources *locov1alpha1.ResourcesSpec,
) (*corev1ac.ResourceRequirementsApplyConfiguration, error) {
	quantities, err := parseResourceList(resources.CPU, resources.Memory)
	if err != nil {
		return nil, err
	}
	return corev1ac.ResourceRequirements().WithRequests(quantities).WithLimits(quantities), nil
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

func systemEnvVars(
	app *locov1alpha1.Application,
	namespace string,
	telemetry TelemetryConfig,
	secretNames []string,
) []*corev1ac.EnvVarApplyConfiguration {
	publicDomain := ""
	if app.Spec.ServiceSpec.Routing != nil {
		publicDomain = app.Spec.ServiceSpec.Routing.HostName
	}
	internalDomain := getInternalDomain(app, namespace)

	locoVars := []corev1.EnvVar{
		{Name: "LOCO_APP_NAME", Value: app.Name},
		{Name: "LOCO_RESOURCE_ID", Value: app.Spec.ResourceID},
		{Name: "LOCO_WORKSPACE_ID", Value: app.Spec.WorkspaceID},
		{Name: "LOCO_DEPLOYMENT_ID", Value: app.Spec.DeploymentID},
		{Name: envRegion, Value: app.Spec.Region},
		{Name: "LOCO_ENVIRONMENT", Value: app.Spec.EnvironmentName},
		{Name: "LOCO_INTERNAL_DOMAIN", Value: internalDomain},
		{Name: "LOCO_PUBLIC_DOMAIN", Value: publicDomain},
	}
	collectorVars := collectorEnvVars(telemetry)
	otelVars := openTelemetryEnvVars(app, telemetry, secretNames)
	vars := slices.Concat(locoVars, collectorVars, otelVars)

	envVars := make([]*corev1ac.EnvVarApplyConfiguration, 0, len(vars))
	for _, v := range vars {
		envVar := corev1ac.EnvVar().WithName(v.Name).WithValue(v.Value)
		envVars = append(envVars, envVar)
	}
	return envVars
}

func plainEnvVars(
	env map[string]string,
	systemVars []*corev1ac.EnvVarApplyConfiguration,
) []*corev1ac.EnvVarApplyConfiguration {
	system := make(map[string]struct{}, len(systemVars))
	for _, systemVar := range systemVars {
		system[*systemVar.Name] = struct{}{}
	}
	names := slices.Sorted(maps.Keys(env))
	envVars := make([]*corev1ac.EnvVarApplyConfiguration, 0, len(names))
	for _, name := range names {
		if _, reserved := system[name]; reserved {
			continue
		}
		envVar := corev1ac.EnvVar().WithName(name).WithValue(env[name])
		envVars = append(envVars, envVar)
	}
	return envVars
}

func healthProbe(hc *locov1alpha1.HealthCheckSpec, port int32) *corev1ac.ProbeApplyConfiguration {
	probePort := intstr.FromInt32(port)
	httpGet := corev1ac.HTTPGetAction().WithPath(hc.Path).WithPort(probePort)
	return corev1ac.Probe().
		WithHTTPGet(httpGet).
		WithInitialDelaySeconds(hc.StartupGracePeriod).
		WithTimeoutSeconds(hc.Timeout).
		WithPeriodSeconds(hc.Interval).
		WithFailureThreshold(hc.FailThreshold)
}

func desiredDeployment(
	app *locov1alpha1.Application,
	namespace string,
	envSecret envSecretMount,
	telemetry TelemetryConfig,
) (*appsv1ac.DeploymentApplyConfiguration, error) {
	name := locov1alpha1.ApplicationName(app.Spec.ResourceID)
	template, err := podTemplate(app, namespace, envSecret, telemetry)
	if err != nil {
		return nil, err
	}

	replicas := app.Spec.ServiceSpec.Resources.Replicas.Min
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

	labels := workloadLabels(app)
	annotations := ownerAnnotations(app)
	return appsv1ac.Deployment(name, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithSpec(deploymentSpec), nil
}

func podTemplate(
	app *locov1alpha1.Application,
	namespace string,
	envSecret envSecretMount,
	telemetry TelemetryConfig,
) (*corev1ac.PodTemplateSpecApplyConfiguration, error) {
	name := locov1alpha1.ApplicationName(app.Spec.ResourceID)
	spec := app.Spec.ServiceSpec
	containerPort := spec.Deployment.Port

	resources, err := containerResources(spec.Resources)
	if err != nil {
		return nil, fmt.Errorf("resources: %w", err)
	}

	systemVars := systemEnvVars(app, namespace, telemetry, envSecret.Names)
	plainVars := plainEnvVars(spec.Deployment.Env, systemVars)
	envVars := slices.Concat(systemVars, plainVars)
	port := corev1ac.ContainerPort().
		WithName("http").
		WithContainerPort(containerPort).
		WithProtocol(corev1.ProtocolTCP)
	containerSecurity := containerSecurityContext()
	container := corev1ac.Container().
		WithName(name).
		WithImage(spec.Deployment.Image).
		WithEnv(envVars...).
		WithPorts(port).
		WithResources(resources).
		WithSecurityContext(containerSecurity)
	podAnnotations := ownerAnnotations(app)
	if revision, ok := app.PlacementRevision(); ok {
		podAnnotations[locov1alpha1.KeyPlacementRevision] = strconv.FormatInt(revision, 10)
	}
	if envSecret.Mounted {
		envSecretName := getEnvSecretName(app)
		secretRef := corev1ac.SecretEnvSource().WithName(envSecretName)
		envFrom := corev1ac.EnvFromSource().WithSecretRef(secretRef)
		container.WithEnvFrom(envFrom)
		podAnnotations[locolabels.AnnotationEnvSecretRevision] = strconv.FormatInt(envSecret.Revision, 10)
	}

	if hc := spec.Deployment.HealthCheck; hc != nil {
		livenessProbe := healthProbe(hc, containerPort)
		readinessProbe := healthProbe(hc, containerPort)
		container.WithLivenessProbe(livenessProbe).WithReadinessProbe(readinessProbe)
	}

	podLabels := workloadLabels(app)
	podLabels[labelAppKubernetesName] = name
	podLabels[managed.LabelComponent] = componentApplication
	addMetricsScrape(spec.Obs, podLabels, podAnnotations)
	podSecurity := podSecurityContext()
	if spec.Deployment.RunAsUser != nil {
		podSecurity = podSecurity.WithRunAsUser(*spec.Deployment.RunAsUser)
	}
	if spec.Deployment.RunAsGroup != nil {
		podSecurity = podSecurity.WithRunAsGroup(*spec.Deployment.RunAsGroup)
	}
	podSpec := corev1ac.PodSpec().
		WithServiceAccountName(name).
		WithAutomountServiceAccountToken(false).
		WithSecurityContext(podSecurity).
		WithRestartPolicy(corev1.RestartPolicyAlways).
		WithContainers(container)
	return corev1ac.PodTemplateSpec().
		WithLabels(podLabels).
		WithAnnotations(podAnnotations).
		WithSpec(podSpec), nil
}

// ensureDeployment ensures the Kubernetes deployment exists and is configured with the spec
// Returns the applied deployment as reported by the API server
func (r *ApplicationReconciler) ensureDeployment(
	ctx context.Context,
	app *locov1alpha1.Application,
	envSecret envSecretMount,
) (*appsv1ac.DeploymentApplyConfiguration, error) {
	dep, err := desiredDeployment(app, r.workspaceNamespace(app), envSecret, r.Telemetry)
	if err != nil {
		return nil, err
	}

	name := locov1alpha1.ApplicationName(app.Spec.ResourceID)
	namespace := r.workspaceNamespace(app)
	slog.DebugContext(ctx, "ensuring deployment", "namespace", namespace, "name", name)

	opts := managed.ApplyOptions()
	if err := r.Apply(ctx, dep, opts...); err != nil {
		return nil, fmt.Errorf("apply deployment %s/%s: %w", namespace, name, err)
	}
	return dep, nil
}

func deploymentReady(dep *appsv1ac.DeploymentApplyConfiguration, replicas int32) bool {
	if dep.Status == nil {
		return false
	}
	generation := ptr.Deref(dep.Generation, 0)
	observed := ptr.Deref(dep.Status.ObservedGeneration, 0)
	updated := ptr.Deref(dep.Status.UpdatedReplicas, 0)
	available := ptr.Deref(dep.Status.AvailableReplicas, 0)
	total := ptr.Deref(dep.Status.Replicas, 0)
	return generation > 0 && observed == generation && updated == replicas && available == replicas && total == replicas
}

func setReplicaCounts(app *locov1alpha1.Application, dep *appsv1ac.DeploymentApplyConfiguration) {
	app.Status.DesiredReplicas = ptr.Deref(dep.Spec.Replicas, 0)
	app.Status.ReadyReplicas = 0
	if dep.Status != nil {
		app.Status.ReadyReplicas = ptr.Deref(dep.Status.ReadyReplicas, 0)
	}
}

// ensureHTTPRoute ensures the HTTPRoute exists for traffic ingress (Envoy Gateway)
func (r *ApplicationReconciler) ensureHTTPRoute(ctx context.Context, app *locov1alpha1.Application) error {
	name := locov1alpha1.ApplicationName(app.Spec.ResourceID)
	namespace := r.workspaceNamespace(app)
	routeName := getRouteName(app)
	routing := app.Spec.ServiceSpec.Routing

	if routing == nil {
		route := &v1Gateway.HTTPRoute{Name: routeName, Namespace: namespace}
		err := r.Delete(ctx, route)
		if err != nil && !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
			return fmt.Errorf("delete HTTPRoute %s/%s: %w", namespace, routeName, err)
		}
		return nil
	}

	slog.DebugContext(ctx, "ensuring HTTPRoute", "namespace", namespace, "name", routeName)

	gatewayNamespace := v1Gateway.Namespace(r.LocoNamespace)
	gatewayName := v1Gateway.ObjectName(r.Gateway)
	parentRef := gatewayac.ParentReference().
		WithName(gatewayName).
		WithNamespace(gatewayNamespace)
	pathMatch := gatewayac.HTTPPathMatch().
		WithType(v1Gateway.PathMatchPathPrefix).
		WithValue(routing.PathPrefix)
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

	labels := managedLabels(app)
	annotations := ownerAnnotations(app)
	route := gatewayac.HTTPRoute(routeName, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithSpec(spec)

	opts := managed.ApplyOptions()
	if err := r.Apply(ctx, route, opts...); err != nil {
		return fmt.Errorf("apply HTTPRoute %s/%s: %w", namespace, routeName, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApplicationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	applicationChanged := predicate.Or[client.Object](
		predicate.GenerationChangedPredicate{},
		predicate.AnnotationChangedPredicate{},
	)
	applicationPredicates := builder.WithPredicates(applicationChanged)
	applicationHandler := handler.EnqueueRequestsFromMapFunc(applicationForObject)
	// Gateways report attachment and acceptance in status. Only desired route changes need repair.
	routeChanged := predicate.Or(applicationChanged, predicate.LabelChangedPredicate{})
	routePredicates := builder.WithPredicates(routeChanged)
	nodeHandler := handler.EnqueueRequestsFromMapFunc(r.applicationPerWorkspace)
	nodeChanges := isolation.NodeRangesChanged()
	nodePredicates := builder.WithPredicates(nodeChanges)
	options := crcontroller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}

	envSecretHandler := handler.EnqueueRequestForOwner(
		mgr.GetScheme(),
		mgr.GetRESTMapper(),
		&locov1alpha1.Application{},
		handler.OnlyControllerOwner(),
	)

	controllerBuilder := ctrl.NewControllerManagedBy(mgr).
		For(&locov1alpha1.Application{}, applicationPredicates).
		Watches(&appsv1.Deployment{}, applicationHandler).
		Watches(&corev1.Pod{}, applicationHandler).
		Watches(&corev1.Service{}, applicationHandler).
		Watches(&v1Gateway.HTTPRoute{}, applicationHandler, routePredicates).
		Watches(&corev1.Secret{}, envSecretHandler).
		Watches(&corev1.Node{}, nodeHandler, nodePredicates)
	if r.copiesPullSecret() {
		pullSecretHandler := handler.EnqueueRequestsFromMapFunc(r.applicationsForPullSecret)
		pullSecretChanges := r.pullSecretChanged()
		pullSecretPredicates := builder.WithPredicates(pullSecretChanges)
		controllerBuilder = controllerBuilder.Watches(&corev1.Secret{}, pullSecretHandler, pullSecretPredicates)
	}
	return controllerBuilder.
		WithOptions(options).
		Named("application").
		Complete(r)
}
