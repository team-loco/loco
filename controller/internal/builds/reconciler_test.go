package builds

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/team-loco/loco/controller/internal/builds/buildstest"
	"github.com/team-loco/loco/controller/internal/isolation"
	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testBuildNamespace  = "builds-envtest"
	testBuildWorkspace  = "ws-build"
	testPushSecretName  = "registry-builder"
	testBuildRepository = "registry.example.com/loco/ws-1/res-1"
	testImageDigest     = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testCacheDigest     = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func testBuild(name string) *locov1alpha1.Build {
	return &locov1alpha1.Build{
		Name: name, Namespace: testBuildNamespace,
		Spec: locov1alpha1.BuildSpec{
			BuildID:         name,
			WorkspaceID:     testBuildWorkspace,
			ResourceID:      "res-1",
			SourceURL:       "https://bucket.example.com/sources/" + name + ".tar.gz?X-Amz-Signature=abc",
			DockerfilePath:  "deploy/Dockerfile.prod",
			ImageRepository: testBuildRepository,
			CacheRef:        testBuildRepository + "@" + testCacheDigest,
		},
	}
}

func chartDefaults() Config {
	raw, err := buildstest.ChartConfigJSON(nil)
	Expect(err).NotTo(HaveOccurred())
	cfg, err := ParseConfig(raw)
	Expect(err).NotTo(HaveOccurred())
	return cfg
}

func testReconciler() *Reconciler {
	cfg := chartDefaults()
	cfg.Namespace = testBuildNamespace
	cfg.PushSecretName = testPushSecretName
	cfg.PrivateEgressCIDRs = []string{"10.1.2.3/32"}
	cfg.RuntimeClassName = "gvisor"
	cfg.NodeSelector = map[string]string{"loco.io/pool": "builds"}
	return &Reconciler{
		Client:         k8sClient,
		Scheme:         k8sClient.Scheme(),
		APIReader:      k8sClient,
		Config:         cfg,
		LocoNamespace:  testNamespace,
		PullSecretName: testPullSecretName,
	}
}

func dockerConfigSecret(name string, payload string) *corev1.Secret {
	return &corev1.Secret{
		Name: name, Namespace: testNamespace,
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(payload)},
	}
}

func reconcileBuild(r *Reconciler, build *locov1alpha1.Build) reconcile.Result {
	key := client.ObjectKeyFromObject(build)
	result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Get(ctx, key, build)).To(Succeed())
	return result
}

func observeBuildJob(r *Reconciler, build *locov1alpha1.Build, job *batchv1.Job) reconcile.Result {
	result, err := r.observeJob(ctx, build, job)
	Expect(err).NotTo(HaveOccurred())
	return result
}

type stalePodClient struct {
	client.Client
	pods []corev1.Pod
}

func (c stalePodClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	podList, ok := list.(*corev1.PodList)
	if !ok {
		return c.Client.List(ctx, list, opts...)
	}
	podList.Items = slices.Clone(c.pods)
	return nil
}

func buildJobFor(build *locov1alpha1.Build) *batchv1.Job {
	job := &batchv1.Job{}
	key := client.ObjectKeyFromObject(build)
	Expect(k8sClient.Get(ctx, key, job)).To(Succeed())
	return job
}

func findContainer(containers []corev1.Container, name string) corev1.Container {
	for _, c := range containers {
		if c.Name == name {
			return c
		}
	}
	Fail("no container " + name)
	return corev1.Container{}
}

func mountNames(c corev1.Container) []string {
	names := make([]string, 0, len(c.VolumeMounts))
	for _, m := range c.VolumeMounts {
		names = append(names, m.Name)
	}
	return names
}

func createBuildPod(job *batchv1.Job, status corev1.PodStatus) *corev1.Pod {
	labels := map[string]string{
		labelJobControllerUID:  string(job.UID),
		labelJobName:           job.Name,
		managed.LabelManagedBy: managed.ManagedByValue,
		labelComponent:         componentBuild,
	}
	pod := &corev1.Pod{
		GenerateName: job.Name + "-",
		Namespace:    job.Namespace,
		Labels:       labels,
		Spec:         *job.Spec.Template.Spec.DeepCopy(),
	}
	pod.Spec.RuntimeClassName = nil
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	pod.Status = status
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
	return pod
}

func terminated(name string, exitCode int32, message string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name: name,
		State: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: exitCode, Message: message},
		},
	}
}

func runningPushStatus() corev1.PodStatus {
	running := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
	return corev1.PodStatus{
		Phase:             corev1.PodRunning,
		ContainerStatuses: []corev1.ContainerStatus{{Name: buildContainerPush, State: running}},
	}
}

func waiting(name, reason string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		Name:  name,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: "details"}},
	}
}

func withCondition(job *batchv1.Job, conditionType batchv1.JobConditionType, reason string) *batchv1.Job {
	observed := job.DeepCopy()
	observed.Status.Conditions = append(observed.Status.Conditions, batchv1.JobCondition{
		Type:   conditionType,
		Status: corev1.ConditionTrue,
		Reason: reason,
	})
	return observed
}

var _ = Describe("Build reconcile", Ordered, func() {
	var pushSecret, pullSecret *corev1.Secret

	BeforeAll(func() {
		namespace := &corev1.Namespace{Name: testBuildNamespace}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		pushSecret = dockerConfigSecret(testPushSecretName, `{"auths":{"registry.example.com":{"auth":"cHVzaA=="}}}`)
		Expect(k8sClient.Create(ctx, pushSecret)).To(Succeed())
		pullSecret = dockerConfigSecret(testPullSecretName, `{"auths":{"registry.example.com":{"auth":"cHVsbA=="}}}`)
		Expect(k8sClient.Create(ctx, pullSecret)).To(Succeed())
	})

	AfterAll(func() {
		Expect(k8sClient.Delete(ctx, pushSecret)).To(Succeed())
		Expect(k8sClient.Delete(ctx, pullSecret)).To(Succeed())
	})

	It("rejects invalid specs and spec changes", func() {
		build := testBuild("invalid-path")
		build.Spec.DockerfilePath = "../Dockerfile"
		Expect(k8sClient.Create(ctx, build)).NotTo(Succeed())

		build = testBuild("tagged-cache")
		build.Spec.CacheRef = testBuildRepository + ":latest"
		Expect(k8sClient.Create(ctx, build)).NotTo(Succeed())

		build = testBuild("immutable")
		Expect(k8sClient.Create(ctx, build)).To(Succeed())
		build.Spec.DockerfilePath = "Dockerfile"
		Expect(k8sClient.Update(ctx, build)).NotTo(Succeed())
		Expect(k8sClient.Delete(ctx, build)).To(Succeed())
	})

	It("creates a locked-down job with secrets only where they are needed", func() {
		r := testReconciler()
		build := testBuild("shape")
		Expect(k8sClient.Create(ctx, build)).To(Succeed())

		reconcileBuild(r, build)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhasePending))

		job := buildJobFor(build)
		Expect(metav1.IsControlledBy(job, build)).To(BeTrue())
		Expect(job.Spec.BackoffLimit).To(HaveValue(BeZero()))
		Expect(job.Spec.ActiveDeadlineSeconds).To(HaveValue(Equal(int64(1800))))
		Expect(job.Spec.TTLSecondsAfterFinished).To(HaveValue(BeNumerically(">=", 120)))
		wantLabels := map[string]string{
			managed.LabelWorkspaceID: testBuildWorkspace,
			managed.LabelResourceID:  "res-1",
			labelBuildID:             "shape",
			managed.LabelManagedBy:   managed.ManagedByValue,
		}
		for key, value := range wantLabels {
			Expect(job.Labels).To(HaveKeyWithValue(key, value))
			Expect(job.Spec.Template.Labels).To(HaveKeyWithValue(key, value))
		}

		spec := job.Spec.Template.Spec
		Expect(spec.AutomountServiceAccountToken).To(HaveValue(BeFalse()))
		Expect(spec.EnableServiceLinks).To(HaveValue(BeFalse()))
		Expect(spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))
		Expect(spec.RuntimeClassName).To(HaveValue(Equal("gvisor")))
		Expect(spec.NodeSelector).To(HaveKeyWithValue("loco.io/pool", "builds"))
		Expect(spec.ShareProcessNamespace).To(BeNil())
		Expect(spec.HostNetwork).To(BeFalse())
		Expect(spec.HostPID).To(BeFalse())
		Expect(spec.SecurityContext.FSGroup).To(HaveValue(Equal(buildUser)))

		Expect(spec.InitContainers).To(HaveLen(2))
		Expect(spec.Containers).To(HaveLen(1))
		fetch := findContainer(spec.InitContainers, buildContainerFetch)
		buildkit := findContainer(spec.InitContainers, buildContainerBuild)
		push := findContainer(spec.Containers, buildContainerPush)
		Expect(spec.InitContainers[0].Name).To(Equal(buildContainerFetch))

		Expect(mountNames(push)).To(ContainElement(buildVolumePushCreds))
		Expect(mountNames(push)).NotTo(ContainElement(buildVolumePullCreds))
		Expect(mountNames(fetch)).To(ContainElement(buildVolumePullCreds))
		Expect(mountNames(fetch)).NotTo(ContainElement(buildVolumePushCreds))
		Expect(mountNames(buildkit)).NotTo(ContainElement(buildVolumePushCreds))
		Expect(mountNames(buildkit)).NotTo(ContainElement(buildVolumePullCreds))
		for _, c := range []corev1.Container{fetch, buildkit, push} {
			Expect(c.EnvFrom).To(BeEmpty())
			for _, env := range c.Env {
				Expect(env.ValueFrom).To(BeNil())
			}
		}
		for _, m := range buildkit.VolumeMounts {
			if m.Name == buildVolumeWorkspace || m.Name == buildVolumeCache {
				Expect(m.ReadOnly).To(BeTrue(), m.Name)
			}
		}
		for _, m := range push.VolumeMounts {
			Expect(m.ReadOnly).To(BeTrue(), m.Name)
		}

		Expect(buildkit.Args).To(ContainElements(
			"--local=dockerfile=/workspace/deploy",
			"--opt=filename=Dockerfile.prod",
			"--local=context=/workspace",
		))
		Expect(buildkit.Env).To(ContainElement(corev1.EnvVar{Name: "BUILDKITD_FLAGS", Value: buildkitFlags}))
		Expect(buildkit.SecurityContext.RunAsUser).To(HaveValue(Equal(buildUser)))
		Expect(buildkit.SecurityContext.RunAsNonRoot).To(HaveValue(BeTrue()))
		Expect(buildkit.SecurityContext.AllowPrivilegeEscalation).To(BeNil())
		Expect(buildkit.SecurityContext.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeUnconfined))
		Expect(buildkit.SecurityContext.AppArmorProfile.Type).To(Equal(corev1.AppArmorProfileTypeUnconfined))
		for _, helper := range []corev1.Container{fetch, push} {
			Expect(helper.SecurityContext.AllowPrivilegeEscalation).To(HaveValue(BeFalse()))
			Expect(helper.SecurityContext.ReadOnlyRootFilesystem).To(HaveValue(BeTrue()))
			Expect(helper.SecurityContext.Capabilities.Drop).To(ContainElement(corev1.Capability("ALL")))
		}

		Expect(push.Args).To(ContainElements(
			"--image-ref="+testBuildRepository+":build-shape",
			"--cache-ref="+testBuildRepository+":buildcache-shape",
			fmt.Sprintf("--max-image-bytes=%d", int64(1<<30)),
		))
		Expect(fetch.Args).To(ContainElement("--cache-ref=" + testBuildRepository + "@" + testCacheDigest))
		Expect(fetch.Env).To(ContainElement(corev1.EnvVar{Name: "SOURCE_URL", Value: build.Spec.SourceURL}))
		downloadTimeout := r.Config.DownloadTimeout.Duration.String()
		Expect(fetch.Env).To(ContainElement(corev1.EnvVar{Name: "DOWNLOAD_TIMEOUT", Value: downloadTimeout}))

		for _, volume := range spec.Volumes {
			if volume.EmptyDir != nil {
				Expect(volume.EmptyDir.SizeLimit).NotTo(BeNil(), volume.Name)
			}
		}

		copied := &corev1.Secret{}
		pushKey := client.ObjectKey{Namespace: testBuildNamespace, Name: buildPushSecret}
		Expect(k8sClient.Get(ctx, pushKey, copied)).To(Succeed())
		Expect(copied.Data).To(Equal(pushSecret.Data))
		pullKey := client.ObjectKey{Namespace: testBuildNamespace, Name: buildPullSecret}
		Expect(k8sClient.Get(ctx, pullKey, copied)).To(Succeed())
		Expect(copied.Data).To(Equal(pullSecret.Data))

		policies := &networkingv1.NetworkPolicyList{}
		Expect(k8sClient.List(ctx, policies, client.InNamespace(testBuildNamespace))).To(Succeed())
		names := make([]string, 0, len(policies.Items))
		for _, policy := range policies.Items {
			names = append(names, policy.Name)
			Expect(policy.Spec.Ingress).To(BeEmpty(), policy.Name)
		}
		wantPolicies := []string{
			isolation.PolicyDefaultDeny,
			isolation.PolicyDNSEgress,
			isolation.PolicyInternetEgress,
			policyPrivateEgress,
		}
		Expect(names).To(ConsistOf(wantPolicies))
		internet := getPolicy(testBuildNamespace, isolation.PolicyInternetEgress)
		ipv4 := internet.Spec.Egress[0].To[0].IPBlock
		Expect(ipv4.CIDR).To(Equal("0.0.0.0/0"))
		Expect(ipv4.Except).To(ContainElements("10.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16"))

		reconcileBuild(r, build)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhasePending))
	})

	It("moves through Running to Succeeded with the pushed digests", func() {
		r := testReconciler()
		build := testBuild("success")
		Expect(k8sClient.Create(ctx, build)).To(Succeed())
		reconcileBuild(r, build)
		job := buildJobFor(build)

		startTime := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
		running := corev1.PodStatus{
			Phase:     corev1.PodPending,
			StartTime: &startTime,
			InitContainerStatuses: []corev1.ContainerStatus{
				terminated(buildContainerFetch, 0, ""),
				{Name: buildContainerBuild, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			},
		}
		pod := createBuildPod(job, running)
		reconcileBuild(r, build)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhaseRunning))
		Expect(build.Status.StartedAt).NotTo(BeNil())
		Expect(build.Status.StartedAt.Time).To(BeTemporally("~", startTime.Time, time.Second))

		result := fmt.Sprintf(`{"imageDigest":%q,"cacheDigest":%q}`, testImageDigest, testCacheDigest)
		pod.Status.Phase = corev1.PodSucceeded
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{terminated(buildContainerPush, 0, result)}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

		original := build.DeepCopy()
		complete := withCondition(job, batchv1.JobComplete, "")
		observeBuildJob(r, build, complete)
		Expect(r.patchBuildStatus(ctx, build, original)).To(Succeed())
		reconcileBuild(r, build)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhaseSucceeded))
		Expect(build.Status.ImageDigest).To(Equal(testImageDigest))
		Expect(build.Status.CacheDigest).To(Equal(testCacheDigest))
		Expect(build.Status.FinishedAt).NotTo(BeNil())

		Expect(k8sClient.Delete(ctx, job)).To(Succeed())
		reconcileBuild(r, build)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhaseSucceeded))
	})

	It("reads the finished pod from the API server when the cache is behind", func() {
		r := testReconciler()
		build := testBuild("stale-pod-cache")
		Expect(k8sClient.Create(ctx, build)).To(Succeed())
		reconcileBuild(r, build)
		job := buildJobFor(build)

		pod := createBuildPod(job, runningPushStatus())
		r.Client = stalePodClient{Client: k8sClient, pods: []corev1.Pod{*pod.DeepCopy()}}
		pushed := fmt.Sprintf(`{"imageDigest":%q}`, testImageDigest)
		pod.Status.Phase = corev1.PodSucceeded
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{terminated(buildContainerPush, 0, pushed)}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

		complete := withCondition(job, batchv1.JobComplete, "")
		observeBuildJob(r, build, complete)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhaseSucceeded))
		Expect(build.Status.ImageDigest).To(Equal(testImageDigest))
	})

	It("waits for the push result when the job completes first", func() {
		r := testReconciler()
		build := testBuild("push-result-pending")
		Expect(k8sClient.Create(ctx, build)).To(Succeed())
		reconcileBuild(r, build)
		job := buildJobFor(build)
		createBuildPod(job, runningPushStatus())

		complete := withCondition(job, batchv1.JobComplete, "")
		result := observeBuildJob(r, build, complete)
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(build.Finished()).To(BeFalse())
	})

	It("fails when the push result cannot be trusted", func() {
		r := testReconciler()
		build := testBuild("bad-result")
		Expect(k8sClient.Create(ctx, build)).To(Succeed())
		reconcileBuild(r, build)
		job := buildJobFor(build)
		status := corev1.PodStatus{
			Phase:             corev1.PodSucceeded,
			ContainerStatuses: []corev1.ContainerStatus{terminated(buildContainerPush, 0, `{"imageDigest":"latest"}`)},
		}
		createBuildPod(job, status)

		complete := withCondition(job, batchv1.JobComplete, "")
		observeBuildJob(r, build, complete)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhaseFailed))
		Expect(build.Status.Message).To(ContainSubstring("invalid image digest"))
		Expect(build.Status.ImageDigest).To(BeEmpty())
	})

	It("reports the failing step and the deadline", func() {
		r := testReconciler()
		build := testBuild("step-failure")
		Expect(k8sClient.Create(ctx, build)).To(Succeed())
		reconcileBuild(r, build)
		job := buildJobFor(build)
		status := corev1.PodStatus{
			Phase: corev1.PodFailed,
			InitContainerStatuses: []corev1.ContainerStatus{
				terminated(buildContainerFetch, 0, ""),
				terminated(buildContainerBuild, 1, "ERROR: process \"/bin/sh -c make\" did not complete successfully"),
			},
		}
		createBuildPod(job, status)

		failed := withCondition(job, batchv1.JobFailed, "BackoffLimitExceeded")
		observeBuildJob(r, build, failed)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhaseFailed))
		Expect(build.Status.Message).To(HavePrefix("build step failed with exit code 1: ERROR"))

		deadline := testBuild("deadline")
		deadline.Status = locov1alpha1.BuildStatus{}
		timedOut := withCondition(job, batchv1.JobFailed, jobReasonDeadline)
		observeBuildJob(r, deadline, timedOut)
		Expect(deadline.Status.Phase).To(Equal(locov1alpha1.BuildPhaseFailed))
		Expect(deadline.Status.Message).To(Equal("build did not finish within 30m0s"))
	})

	It("fails promptly and removes the job when a step cannot start", func() {
		r := testReconciler()
		build := testBuild("stuck")
		Expect(k8sClient.Create(ctx, build)).To(Succeed())
		reconcileBuild(r, build)
		job := buildJobFor(build)
		status := corev1.PodStatus{
			Phase: corev1.PodPending,
			InitContainerStatuses: []corev1.ContainerStatus{
				terminated(buildContainerFetch, 0, ""),
				waiting(buildContainerBuild, "ImagePullBackOff"),
			},
		}
		createBuildPod(job, status)

		reconcileBuild(r, build)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhaseFailed))
		Expect(build.Status.Message).To(Equal("build step could not start: ImagePullBackOff: details"))
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(job), &batchv1.Job{})
		if err == nil {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).To(Succeed())
			Expect(job.DeletionTimestamp).NotTo(BeNil())
		} else {
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}
	})

	It("marks a build canceled when its job is deleted while it runs", func() {
		r := testReconciler()
		build := testBuild("deleted-job")
		Expect(k8sClient.Create(ctx, build)).To(Succeed())
		reconcileBuild(r, build)
		job := buildJobFor(build)
		background := client.PropagationPolicy(metav1.DeletePropagationBackground)
		Expect(k8sClient.Delete(ctx, job, background)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(job), &batchv1.Job{})
			return apierrors.IsNotFound(err)
		}).Should(BeTrue())

		reconcileBuild(r, build)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhaseCanceled))
	})

	It("fails builds when no push secret is configured", func() {
		r := testReconciler()
		r.Config.PushSecretName = ""
		build := testBuild("unconfigured")
		Expect(k8sClient.Create(ctx, build)).To(Succeed())
		reconcileBuild(r, build)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhaseFailed))
		Expect(build.Status.Message).To(Equal(buildMessageNoPushSecret))
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(build), &batchv1.Job{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("leaves a foreign job alone", func() {
		r := testReconciler()
		build := testBuild("foreign")
		foreign := &batchv1.Job{
			Name: build.Name, Namespace: testBuildNamespace,
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Containers:    []corev1.Container{{Name: "x", Image: "busybox"}},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, foreign)).To(Succeed())
		Expect(k8sClient.Create(ctx, build)).To(Succeed())
		reconcileBuild(r, build)
		Expect(build.Status.Phase).To(Equal(locov1alpha1.BuildPhaseFailed))
		Expect(strings.Contains(build.Status.Message, "does not belong")).To(BeTrue())
	})
})
