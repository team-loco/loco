package controller

import (
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
)

const runtimePrivateDetail = "private detail"

func TestRuntimeProblem(t *testing.T) {
	tests := []struct {
		name   string
		status corev1.PodStatus
		want   string
	}{
		{
			"crash",
			corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason:  "CrashLoopBackOff",
								Message: runtimePrivateDetail,
							},
						},
					},
				},
			},
			"crash_looping",
		},
		{
			"memory takes precedence",
			corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
						},
						LastTerminationState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{Reason: reasonOOMKilled},
						},
					},
				},
			},
			"out_of_memory",
		},
		{
			"image",
			corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"}}},
				},
			},
			"image_pull_failed",
		},
		{
			"capacity",
			corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  corev1.PodReasonUnschedulable,
						Message: runtimePrivateDetail,
					},
				},
			},
			"unschedulable",
		},
		{
			"healthy ignores old oom",
			corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Ready: true,
						LastTerminationState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{Reason: reasonOOMKilled},
						},
					},
				},
			},
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, message := runtimeProblem([]corev1.Pod{{Status: tt.status}}, time.Minute)
			if reason != tt.want {
				t.Fatalf("reason = %q, want %q", reason, tt.want)
			}
			if reason != "" && (message == "" || message == runtimePrivateDetail) {
				t.Fatalf("message = %q", message)
			}
		})
	}
}

func TestRuntimePodRoutesToApplication(t *testing.T) {
	application := testApplication()
	deployment, err := desiredDeployment(
		application,
		testWorkspaceNamespace(application),
		envSecretMount{},
		testTelemetry(),
	)
	if err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{Annotations: deployment.Spec.Template.Annotations}
	requests := applicationForObject(t.Context(), pod)
	if len(requests) != 1 || requests[0].Name != application.Name || requests[0].Namespace != application.Namespace {
		t.Fatalf("pod watch requests = %v", requests)
	}
}

func TestRuntimeReadinessAllowsTheConfiguredStartupWindow(t *testing.T) {
	started := true
	pod := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{
			Name:           "runtime-service",
			ReadinessProbe: &corev1.Probe{InitialDelaySeconds: 60, PeriodSeconds: 10, FailureThreshold: 3},
		},
	}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
		{
			Name:    "runtime-service",
			Started: &started,
			State:   corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()}},
		},
	}}}
	reason, _ := runtimeProblem([]corev1.Pod{pod}, time.Minute)
	if reason != "" {
		t.Fatalf("startup reported %q", reason)
	}
	pod.Status.ContainerStatuses[0].State.Running.StartedAt = metav1.NewTime(time.Now().Add(-time.Hour))
	reason, _ = runtimeProblem([]corev1.Pod{pod}, time.Minute)
	if reason != locov1alpha1.RuntimeHealthCheckFailing {
		t.Fatalf("unready after grace reported %q", reason)
	}
}

func TestRuntimeCrashPersistsWhileAnUnreadyReplicaRestarts(t *testing.T) {
	pod := corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
		{
			RestartCount:         4,
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
		},
	}}}
	for _, state := range []corev1.ContainerState{
		{Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff}},
		{Running: &corev1.ContainerStateRunning{}},
		{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
	} {
		pod.Status.ContainerStatuses[0].State = state
		reason, _ := runtimeProblem([]corev1.Pod{pod}, time.Minute)
		if reason != locov1alpha1.RuntimeCrashLooping {
			t.Fatalf("restart state %+v cleared the crash: %q", state, reason)
		}
	}
}

func TestRuntimeIgnoresAnOlderPlacementWithTheSameImage(t *testing.T) {
	application := testApplication()
	application.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
	pod := &corev1.Pod{
		Name:        "older-placement",
		Namespace:   testWorkspaceNamespace(application),
		Labels:      map[string]string{labelApp: locov1alpha1.ApplicationName(application.Spec.ResourceID)},
		Annotations: map[string]string{locov1alpha1.KeyPlacementRevision: "1"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Image: application.Spec.ServiceSpec.Deployment.Image}},
		},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff},
		}}}},
	}
	reconciler := &ApplicationReconciler{
		Client:                   fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pod).Build(),
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
	}
	if err := reconciler.observeRuntimeProblem(t.Context(), application, ""); err != nil {
		t.Fatal(err)
	}
	if application.Status.RuntimeReason != "" {
		t.Fatalf("old revision reported %q", application.Status.RuntimeReason)
	}
	pod.Annotations[locov1alpha1.KeyPlacementRevision] = "2"
	if err := reconciler.Update(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.observeRuntimeProblem(t.Context(), application, ""); err != nil {
		t.Fatal(err)
	}
	if application.Status.RuntimeReason != locov1alpha1.RuntimeCrashLooping {
		t.Fatalf("current revision reported %q", application.Status.RuntimeReason)
	}
}

func TestRuntimeProcessOnlyCrashRequiresASustainedRunToRecover(t *testing.T) {
	pod := corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
		Ready: true, RestartCount: 5,
		State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
	}}}}
	reason, _ := runtimeProblem([]corev1.Pod{pod}, time.Minute)
	if reason != locov1alpha1.RuntimeCrashLooping {
		t.Fatalf("brief ready run cleared crash: %q", reason)
	}
	pod.Status.ContainerStatuses[0].State.Running.StartedAt = metav1.NewTime(time.Now().Add(-time.Hour))
	reason, _ = runtimeProblem([]corev1.Pod{pod}, time.Minute)
	if reason != "" {
		t.Fatalf("sustained recovery reported %q", reason)
	}
}

func TestRuntimeRecoveredExitDoesNotOverrideALaterHealthFailure(t *testing.T) {
	started := true
	pod := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name: "recovered-service", ReadinessProbe: &corev1.Probe{PeriodSeconds: 10, FailureThreshold: 3},
	}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
		{
			Name:         "recovered-service",
			Started:      &started,
			RestartCount: 4,
			State: corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(time.Now().Add(-time.Hour))},
			},
			LastTerminationState: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{Reason: reasonOOMKilled, ExitCode: 137},
			},
		},
	}}}
	reason, _ := runtimeProblem([]corev1.Pod{pod}, time.Minute)
	if reason != locov1alpha1.RuntimeHealthCheckFailing {
		t.Fatalf("historical exit overrode health failure: %q", reason)
	}
}

func TestUnreadyCrashDoesNotRecoverBetweenSlowLivenessRestarts(t *testing.T) {
	startedAt := time.Now().Add(-70 * time.Second)
	pod := corev1.Pod{Status: corev1.PodStatus{
		Conditions: []corev1.PodCondition{{
			Type: corev1.ContainersReady, Status: corev1.ConditionFalse,
			LastTransitionTime: metav1.NewTime(startedAt.Add(-time.Minute)),
		}},
		ContainerStatuses: []corev1.ContainerStatus{{
			RestartCount: 1,
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{
				StartedAt: metav1.NewTime(startedAt),
			}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
		}},
	}}
	reason, _ := runtimeProblem([]corev1.Pod{pod}, time.Minute)
	if reason != locov1alpha1.RuntimeCrashLooping {
		t.Fatalf("unready retry cleared the crash after the recovery window: %q", reason)
	}
	pod.Status.ContainerStatuses[0].Ready = true
	reason, _ = runtimeProblem([]corev1.Pod{pod}, time.Minute)
	if reason != "" {
		t.Fatalf("sustained ready run retained crash: %q", reason)
	}
	// A new failure after it became Ready has a transition newer than its run.
	pod.Status.ContainerStatuses[0].Ready = false
	pod.Status.Conditions[0].LastTransitionTime = metav1.NewTime(startedAt.Add(time.Minute))
	reason, _ = runtimeProblem([]corev1.Pod{pod}, time.Minute)
	if reason != "" {
		t.Fatalf("later unreadiness revived historical crash: %q", reason)
	}
	pod.Status.Conditions[0].LastTransitionTime = metav1.Time{}
	reason, _ = runtimeProblem([]corev1.Pod{pod}, time.Minute)
	if reason != "" {
		t.Fatalf("missing readiness history revived historical crash: %q", reason)
	}
}
