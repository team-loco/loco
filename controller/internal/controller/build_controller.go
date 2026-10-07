package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	labelJobName             = "batch.kubernetes.io/job-name"
	labelJobControllerUID    = "batch.kubernetes.io/controller-uid"
	policyPrivateEgress      = "allow-private-egress"
	maxBuildMessageLength    = 1024
	buildCacheLagRequeue     = time.Second
	jobReasonDeadline        = "DeadlineExceeded"
	buildMessageWaiting      = "waiting for the build pod to start"
	buildMessageRunning      = "building"
	buildMessageSucceeded    = "build succeeded"
	buildMessageJobDeleted   = "the build job was deleted before it finished"
	buildMessageNoPushSecret = "builds are not configured on this cluster: no registry push secret"
)

var (
	errBuildPodGone = errors.New("the build pod is gone, so the pushed digest is unknown")
	sha256Digest    = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	stuckReasons    = []string{
		"CreateContainerError",
		"CreateContainerConfigError",
		"ErrImagePull",
		"ErrImageNeverPull",
		"ImagePullBackOff",
		"InvalidImageName",
	}
)

type BuildReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	APIReader      client.Reader
	Config         BuildConfig
	LocoNamespace  string
	PullSecretName string
}

type buildResult struct {
	ImageDigest string `json:"imageDigest"`
	CacheDigest string `json:"cacheDigest"`
}

// +kubebuilder:rbac:groups=infra.loco.io,resources=builds,verbs=get;list;watch
// +kubebuilder:rbac:groups=infra.loco.io,resources=builds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infra.loco.io,resources=builds/finalizers,verbs=update

func (r *BuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	build := &locov1alpha1.Build{}
	if err := r.Get(ctx, req.NamespacedName, build); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get build: %w", err)
	}
	if build.Finished() || !build.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	original := build.DeepCopy()
	result, err := r.reconcileBuild(ctx, build)
	if statusErr := r.patchBuildStatus(ctx, build, original); statusErr != nil {
		return ctrl.Result{}, errors.Join(err, statusErr)
	}
	return result, err
}

func (r *BuildReconciler) reconcileBuild(ctx context.Context, build *locov1alpha1.Build) (ctrl.Result, error) {
	key := client.ObjectKeyFromObject(build)
	job := &batchv1.Job{}
	err := r.Get(ctx, key, job)
	if apierrors.IsNotFound(err) {
		if build.Status.Phase == "" {
			return r.startBuild(ctx, build)
		}
		return r.handleMissingJob(ctx, build)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("get job %s: %w", key, err)
	}
	if !metav1.IsControlledBy(job, build) {
		message := fmt.Sprintf("job %s already exists and does not belong to this build", key)
		finishBuild(build, locov1alpha1.BuildPhaseFailed, message)
		return ctrl.Result{}, nil
	}
	return r.observeJob(ctx, build, job)
}

func (r *BuildReconciler) handleMissingJob(ctx context.Context, build *locov1alpha1.Build) (ctrl.Result, error) {
	key := client.ObjectKeyFromObject(build)
	live := &batchv1.Job{}
	err := r.APIReader.Get(ctx, key, live)
	if err == nil {
		return ctrl.Result{RequeueAfter: buildCacheLagRequeue}, nil
	}
	if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("get job %s: %w", key, err)
	}
	finishBuild(build, locov1alpha1.BuildPhaseCanceled, buildMessageJobDeleted)
	return ctrl.Result{}, nil
}

func (r *BuildReconciler) startBuild(ctx context.Context, build *locov1alpha1.Build) (ctrl.Result, error) {
	if r.Config.PushSecretName == "" {
		finishBuild(build, locov1alpha1.BuildPhaseFailed, buildMessageNoPushSecret)
		return ctrl.Result{}, nil
	}
	if err := r.ensureBuildNetworkPolicies(ctx); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.copySecret(ctx, r.Config.PushSecretName, buildPushSecret); err != nil {
		return ctrl.Result{}, err
	}
	if r.PullSecretName != "" {
		if err := r.copySecret(ctx, r.PullSecretName, buildPullSecret); err != nil {
			return ctrl.Result{}, err
		}
	}
	job, err := r.buildJob(build)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return ctrl.Result{RequeueAfter: buildCacheLagRequeue}, nil
		}
		return ctrl.Result{}, fmt.Errorf("create job %s/%s: %w", job.Namespace, job.Name, err)
	}
	slog.InfoContext(ctx, "build job created", "namespace", job.Namespace, "name", job.Name)
	build.Status.Phase = locov1alpha1.BuildPhasePending
	build.Status.Message = buildMessageWaiting
	return ctrl.Result{}, nil
}

func (r *BuildReconciler) observeJob(
	ctx context.Context,
	build *locov1alpha1.Build,
	job *batchv1.Job,
) (ctrl.Result, error) {
	if jobConditionTrue(job, batchv1.JobComplete) {
		return r.completeBuild(ctx, build, job)
	}
	if cond := jobCondition(job, batchv1.JobFailed); cond != nil && cond.Status == corev1.ConditionTrue {
		pod, err := newestBuildPod(ctx, r.APIReader, job)
		if err != nil {
			return ctrl.Result{}, err
		}
		message := r.failureMessage(cond, pod)
		finishBuild(build, locov1alpha1.BuildPhaseFailed, message)
		return ctrl.Result{}, nil
	}
	pod, err := newestBuildPod(ctx, r.Client, job)
	if err != nil {
		return ctrl.Result{}, err
	}
	if pod == nil {
		return ctrl.Result{}, nil
	}
	if message, stuck := stuckContainer(pod); stuck {
		finishBuild(build, locov1alpha1.BuildPhaseFailed, message)
		propagation := client.PropagationPolicy(metav1.DeletePropagationBackground)
		if err := r.Delete(ctx, job, propagation); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete stuck job %s/%s: %w", job.Namespace, job.Name, err)
		}
		return ctrl.Result{}, nil
	}
	if podStarted(pod) && build.Status.Phase != locov1alpha1.BuildPhaseRunning {
		build.Status.Phase = locov1alpha1.BuildPhaseRunning
		build.Status.Message = buildMessageRunning
		started := metav1.Now()
		if pod.Status.StartTime != nil {
			started = *pod.Status.StartTime
		}
		build.Status.StartedAt = &started
	}
	return ctrl.Result{}, nil
}

func (r *BuildReconciler) completeBuild(
	ctx context.Context,
	build *locov1alpha1.Build,
	job *batchv1.Job,
) (ctrl.Result, error) {
	pod, err := newestBuildPod(ctx, r.APIReader, job)
	if err != nil {
		return ctrl.Result{}, err
	}
	if pod == nil {
		finishBuild(build, locov1alpha1.BuildPhaseFailed, errBuildPodGone.Error())
		return ctrl.Result{}, nil
	}
	terminated := pushStatus(pod)
	if terminated == nil {
		return ctrl.Result{RequeueAfter: buildCacheLagRequeue}, nil
	}
	result, err := parsePushResult(terminated.Message)
	if err != nil {
		finishBuild(build, locov1alpha1.BuildPhaseFailed, err.Error())
		return ctrl.Result{}, nil
	}
	build.Status.ImageDigest = result.ImageDigest
	build.Status.CacheDigest = result.CacheDigest
	finishBuild(build, locov1alpha1.BuildPhaseSucceeded, buildMessageSucceeded)
	return ctrl.Result{}, nil
}

func newestBuildPod(ctx context.Context, reader client.Reader, job *batchv1.Job) (*corev1.Pod, error) {
	pods := &corev1.PodList{}
	selector := client.MatchingLabels{labelJobControllerUID: string(job.UID)}
	namespace := client.InNamespace(job.Namespace)
	if err := reader.List(ctx, pods, namespace, selector); err != nil {
		return nil, fmt.Errorf("list pods of job %s/%s: %w", job.Namespace, job.Name, err)
	}
	var newest *corev1.Pod
	for i := range pods.Items {
		pod := &pods.Items[i]
		if newest == nil || newest.CreationTimestamp.Before(&pod.CreationTimestamp) {
			newest = pod
		}
	}
	return newest, nil
}

func jobCondition(job *batchv1.Job, conditionType batchv1.JobConditionType) *batchv1.JobCondition {
	for i := range job.Status.Conditions {
		if job.Status.Conditions[i].Type == conditionType {
			return &job.Status.Conditions[i]
		}
	}
	return nil
}

func jobConditionTrue(job *batchv1.Job, conditionType batchv1.JobConditionType) bool {
	cond := jobCondition(job, conditionType)
	return cond != nil && cond.Status == corev1.ConditionTrue
}

func podStarted(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodRunning {
		return true
	}
	for _, status := range pod.Status.InitContainerStatuses {
		if status.State.Running != nil || status.State.Terminated != nil {
			return true
		}
	}
	return false
}

func allContainerStatuses(pod *corev1.Pod) []corev1.ContainerStatus {
	statuses := slices.Clone(pod.Status.InitContainerStatuses)
	return append(statuses, pod.Status.ContainerStatuses...)
}

func stuckContainer(pod *corev1.Pod) (string, bool) {
	for _, status := range allContainerStatuses(pod) {
		waiting := status.State.Waiting
		if waiting == nil || !slices.Contains(stuckReasons, waiting.Reason) {
			continue
		}
		message := fmt.Sprintf("%s step could not start: %s", status.Name, waiting.Reason)
		if waiting.Message != "" {
			message += ": " + waiting.Message
		}
		return truncateMessage(message), true
	}
	return "", false
}

func (r *BuildReconciler) failureMessage(cond *batchv1.JobCondition, pod *corev1.Pod) string {
	if cond.Reason == jobReasonDeadline {
		return fmt.Sprintf("build did not finish within %s", r.Config.Timeout.Duration)
	}
	if pod != nil {
		for _, status := range allContainerStatuses(pod) {
			terminated := status.State.Terminated
			if terminated == nil || terminated.ExitCode == 0 {
				continue
			}
			message := fmt.Sprintf("%s step failed with exit code %d", status.Name, terminated.ExitCode)
			if terminated.Message != "" {
				message += ": " + terminated.Message
			}
			return truncateMessage(message)
		}
	}
	if cond.Message != "" {
		return truncateMessage("build failed: " + cond.Message)
	}
	return "build failed"
}

func truncateMessage(message string) string {
	if len(message) <= maxBuildMessageLength {
		return message
	}
	return "..." + message[len(message)-maxBuildMessageLength:]
}

func pushStatus(pod *corev1.Pod) *corev1.ContainerStateTerminated {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == buildContainerPush && status.State.Terminated != nil {
			return status.State.Terminated
		}
	}
	return nil
}

func parsePushResult(message string) (buildResult, error) {
	var result buildResult
	if err := json.Unmarshal([]byte(message), &result); err != nil {
		return buildResult{}, fmt.Errorf("push step reported an unreadable result: %w", err)
	}
	if !sha256Digest.MatchString(result.ImageDigest) {
		return buildResult{}, fmt.Errorf("push step reported an invalid image digest %q", result.ImageDigest)
	}
	if result.CacheDigest != "" && !sha256Digest.MatchString(result.CacheDigest) {
		return buildResult{}, fmt.Errorf("push step reported an invalid cache digest %q", result.CacheDigest)
	}
	return result, nil
}

func finishBuild(build *locov1alpha1.Build, phase, message string) {
	now := metav1.Now()
	build.Status.Phase = phase
	build.Status.Message = message
	build.Status.FinishedAt = &now
}

func (r *BuildReconciler) patchBuildStatus(
	ctx context.Context,
	build *locov1alpha1.Build,
	original *locov1alpha1.Build,
) error {
	if equality.Semantic.DeepEqual(original.Status, build.Status) {
		return nil
	}
	patch := client.MergeFrom(original)
	if err := r.Status().Patch(ctx, build, patch); err != nil {
		return fmt.Errorf("patch build status: %w", err)
	}
	return nil
}

func (r *BuildReconciler) copySecret(ctx context.Context, sourceName, targetName string) error {
	sourceKey := client.ObjectKey{Namespace: r.LocoNamespace, Name: sourceName}
	source := &corev1.Secret{}
	if err := r.APIReader.Get(ctx, sourceKey, source); err != nil {
		return fmt.Errorf("get registry secret %s: %w", sourceKey, err)
	}
	if source.Type != corev1.SecretTypeDockerConfigJson {
		return fmt.Errorf(
			"registry secret %s has type %s, want %s",
			sourceKey,
			source.Type,
			corev1.SecretTypeDockerConfigJson,
		)
	}
	dockerConfig, ok := source.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return fmt.Errorf("registry secret %s has no %s key", sourceKey, corev1.DockerConfigJsonKey)
	}
	labels := map[string]string{labelManagedBy: managedByValue}
	data := map[string][]byte{corev1.DockerConfigJsonKey: dockerConfig}
	secret := corev1ac.Secret(targetName, r.Config.Namespace).
		WithLabels(labels).
		WithType(corev1.SecretTypeDockerConfigJson).
		WithData(data)
	opts := applyOptions()
	if err := r.Apply(ctx, secret, opts...); err != nil {
		return fmt.Errorf("apply secret %s/%s: %w", r.Config.Namespace, targetName, err)
	}
	return nil
}

func (r *BuildReconciler) buildNetworkPolicies(
	exclusions egressExclusionSet,
) []*networkingv1ac.NetworkPolicyApplyConfiguration {
	namespace := r.Config.Namespace
	labels := map[string]string{labelManagedBy: managedByValue}
	denyAll := denyAllSpec()
	dnsEgress := dnsEgressSpec()
	internetEgress := internetEgressSpec(exclusions)
	policies := make([]*networkingv1ac.NetworkPolicyApplyConfiguration, 0, 4)
	policies = append(policies,
		namespacePolicy(policyDefaultDeny, namespace, labels, denyAll),
		namespacePolicy(policyDNSEgress, namespace, labels, dnsEgress),
		namespacePolicy(policyInternetEgress, namespace, labels, internetEgress),
	)
	if len(r.Config.PrivateEgressCIDRs) == 0 {
		return policies
	}
	peers := make([]*networkingv1ac.NetworkPolicyPeerApplyConfiguration, 0, len(r.Config.PrivateEgressCIDRs))
	for _, cidr := range r.Config.PrivateEgressCIDRs {
		block := networkingv1ac.IPBlock().WithCIDR(cidr)
		peer := networkingv1ac.NetworkPolicyPeer().WithIPBlock(block)
		peers = append(peers, peer)
	}
	privateRule := networkingv1ac.NetworkPolicyEgressRule().WithTo(peers...)
	privateEgress := networkingv1ac.NetworkPolicySpec().
		WithPolicyTypes(networkingv1.PolicyTypeEgress).
		WithEgress(privateRule)
	privatePolicy := namespacePolicy(policyPrivateEgress, namespace, labels, privateEgress)
	return append(policies, privatePolicy)
}

func (r *BuildReconciler) ensureBuildNetworkPolicies(ctx context.Context) error {
	exclusions, err := discoverEgressExclusions(ctx, r.Client)
	if err != nil {
		return err
	}
	policies := r.buildNetworkPolicies(exclusions)
	return applyPolicies(ctx, r.Client, policies)
}

func buildForPod(_ context.Context, obj client.Object) []reconcile.Request {
	labels := obj.GetLabels()
	if labels[labelComponent] != componentBuild {
		return nil
	}
	jobName, ok := labels[labelJobName]
	if !ok || jobName == "" {
		return nil
	}
	key := types.NamespacedName{Namespace: obj.GetNamespace(), Name: jobName}
	return []reconcile.Request{{NamespacedName: key}}
}

func (r *BuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.LocoNamespace == "" && (r.Config.PushSecretName != "" || r.PullSecretName != "") {
		return fmt.Errorf("build registry secrets require %s to be set", EnvLocoNamespace)
	}
	podHandler := handler.EnqueueRequestsFromMapFunc(buildForPod)
	return ctrl.NewControllerManagedBy(mgr).
		For(&locov1alpha1.Build{}).
		Owns(&batchv1.Job{}).
		Watches(&corev1.Pod{}, podHandler).
		Named("build").
		Complete(r)
}
