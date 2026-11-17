package controller

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
)

func TestReplicaState(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	base := corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
	for _, tt := range []struct {
		reason string
		want   loco.ReplicaState
	}{
		{"", loco.ReplicaServing},
		{reasonContainerCreating, loco.ReplicaStarting},
		{reasonPodInitializing, loco.ReplicaStarting},
		{reasonErrImagePull, loco.ReplicaFailing},
		{reasonImagePullBackOff, loco.ReplicaFailing},
		{reasonInvalidImageName, loco.ReplicaFailing},
		{reasonCreateContainerConfigError, loco.ReplicaFailing},
		{reasonCrashLoopBackOff, loco.ReplicaFailing},
	} {
		t.Run(tt.reason, func(t *testing.T) {
			p := base.DeepCopy()
			if tt.reason != "" {
				p.Status.ContainerStatuses = []corev1.ContainerStatus{
					{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: tt.reason}}},
				}
			}
			if got := replicaState(p, nil, time.Minute, now); got.State != tt.want {
				t.Fatalf("state=%s want %s", got.State, tt.want)
			}
		})
	}
	p := base.DeepCopy()
	p.Status.Conditions = p.Status.Conditions[1:]
	if replicaState(p, nil, time.Minute, now).State != loco.ReplicaScheduling {
		t.Fatal("scheduling must precede serving")
	}
	p.Status.ContainerStatuses = []corev1.ContainerStatus{
		{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff}}},
	}
	if replicaState(p, nil, time.Minute, now).State != loco.ReplicaFailing {
		t.Fatal("failure must precede scheduling")
	}
	p = base.DeepCopy()
	p.Status.Conditions[1].Status = corev1.ConditionUnknown
	if replicaState(p, nil, time.Minute, now).State != loco.ReplicaUnknown {
		t.Fatal("unknown readiness")
	}
	for _, tt := range []struct {
		name   string
		offset time.Duration
		reason string
		want   loco.ReplicaState
	}{
		{"early", -2 * time.Second, "", loco.ReplicaStopped},
		{"half second", -time.Second / 2, "", loco.ReplicaKilled},
		{"deadline", 0, "", loco.ReplicaKilled},
		{"oom", 0, reasonOOMKilled, loco.ReplicaStopped},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := base.DeepCopy()
			deadline := metav1.NewTime(now)
			p.DeletionTimestamp = &deadline
			p.DeletionGracePeriodSeconds = new(int64(35))
			p.Status.ContainerStatuses = []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode:   137,
							Reason:     tt.reason,
							FinishedAt: metav1.NewTime(now.Add(tt.offset)),
						},
					},
				},
			}
			got := replicaState(p, nil, time.Minute, now)
			if got.State != tt.want || got.ExitCode == nil || *got.ExitCode != 137 || got.StateDeadline != nil {
				t.Fatalf("shutdown=%+v", got)
			}
		})
	}
	for _, drain := range []int64{0, 5} {
		p := base.DeepCopy()
		deadline := metav1.NewTime(now.Add(33 * time.Second))
		p.DeletionTimestamp = &deadline
		p.DeletionGracePeriodSeconds = new(int64(35))
		if drain > 0 {
			p.Spec.Containers = []corev1.Container{
				{
					Lifecycle: &corev1.Lifecycle{
						PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: drain}},
					},
				},
			}
		}
		p.Status.ContainerStatuses = []corev1.ContainerStatus{
			{State: corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{}}},
		}
		want := deadline.Time
		if drain > 0 {
			want = now.Add(3 * time.Second)
		}
		got := replicaState(p, nil, time.Minute, now)
		if got.State != loco.ReplicaStopping || got.StateDeadline == nil || !got.StateDeadline.Time.Equal(want) {
			t.Fatalf("stopping=%+v want deadline %s", got, want)
		}
	}
}
func TestRolloutAvailable(t *testing.T) {
	app := &loco.Application{
		Annotations: map[string]string{loco.KeyPlacementRevision: "2"},
		Status:      loco.ApplicationStatus{ObservedPlacementRevision: 2},
	}
	dep := appsv1ac.Deployment("app", "ns").
		WithGeneration(3).
		WithSpec(appsv1ac.DeploymentSpec().
			WithReplicas(2)).
		WithStatus(appsv1ac.DeploymentStatus().
			WithObservedGeneration(3).
			WithUpdatedReplicas(2).
			WithAvailableReplicas(2).
			WithReplicas(2))
	if !rolloutAvailable(dep, app) {
		t.Fatal("equal counts unavailable")
	}
	dep.Status.WithObservedGeneration(2)
	if rolloutAvailable(dep, app) {
		t.Fatal("accepted generation lag")
	}
	dep.Status.WithObservedGeneration(3).
		WithReplicas(3)
	if rolloutAvailable(dep, app) {
		t.Fatal("accepted old counted replica")
	}
	dep.Status.WithReplicas(2)
	app.Status.ObservedPlacementRevision = 1
	if rolloutAvailable(dep, app) {
		t.Fatal("accepted revision mismatch")
	}
}

func TestReplicaDetailClipping(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Message: strings.Repeat("界", 200)},
			},
		},
	}
	got := replicaState(pod, nil, time.Minute, time.Now())
	if len(got.Detail) > 512 || !utf8.ValidString(got.Detail) {
		t.Fatalf("detail has %d bytes or invalid UTF-8", len(got.Detail))
	}
}

func TestReplicaRecoveryStates(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		name   string
		status corev1.ContainerStatus
		want   loco.ReplicaState
	}{
		{"active retry", corev1.ContainerStatus{RestartCount: 1,
			State: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}}, loco.ReplicaFailing},
		{"recovering", corev1.ContainerStatus{Ready: true,
			State: corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(now.Add(-10 * time.Second))}},
			LastTerminationState: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}}, loco.ReplicaFailing},
		{"recovered", corev1.ContainerStatus{Ready: true,
			State: corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(now.Add(-2 * time.Minute))}},
			LastTerminationState: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}}, loco.ReplicaServing},
		{"running unready", corev1.ContainerStatus{State: corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(now)}}}, loco.ReplicaStarting},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
						{Type: corev1.PodReady, Status: corev1.ConditionTrue},
					},
					ContainerStatuses: []corev1.ContainerStatus{tt.status},
				},
			}
			if got := replicaState(pod, nil, time.Minute, now); got.State != tt.want {
				t.Fatalf("state=%s want %s", got.State, tt.want)
			}
		})
	}
}

func TestReplicaConfiguredRecovery(t *testing.T) {
	now := time.Now()
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Ready: true,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(now.Add(-90 * time.Second))},
					},
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
					},
				},
			},
		},
	}
	if got := replicaState(pod, nil, 2*time.Minute, now); got.State != loco.ReplicaFailing {
		t.Fatalf("configured recovery = %+v", got)
	}
	if got := replicaState(pod, nil, time.Minute, now); got.State != loco.ReplicaServing {
		t.Fatalf("recovered = %+v", got)
	}
}

func TestShutdownDetailStable(t *testing.T) {
	now := time.Now()
	pod := &corev1.Pod{
		DeletionTimestamp:          new(metav1.NewTime(now.Add(35 * time.Second))),
		DeletionGracePeriodSeconds: new(int64(35)),
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Lifecycle: &corev1.Lifecycle{
						PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: 5}},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			},
		},
	}
	for _, offset := range []time.Duration{0, 10 * time.Second} {
		a := replicaState(pod, nil, time.Minute, now.Add(offset))
		b := replicaState(pod, nil, time.Minute, now.Add(offset+time.Second))
		if a.Detail != b.Detail || !a.StateDeadline.Equal(b.StateDeadline) {
			t.Fatalf("unstable shutdown: %+v -> %+v", a, b)
		}
	}
}

func TestReplicaPullState(t *testing.T) {
	now := time.Now()
	at := metav1.NewTime(now.Add(-time.Second))
	for _, tt := range []struct {
		name    string
		waiting string
		event   *pullEvent
		want    loco.ReplicaState
		detail  string
	}{
		{"pulling", reasonContainerCreating, &pullEvent{reason: pullReasonPulling, at: at}, loco.ReplicaPulling, ""},
		{"initializing", reasonPodInitializing, &pullEvent{reason: pullReasonPulling, at: at}, loco.ReplicaPulling, ""},
		{"missing", reasonContainerCreating, nil, loco.ReplicaStarting, ""},
		{"pulled", reasonContainerCreating,
			&pullEvent{reason: pullReasonPulled, detail: "Successfully pulled image in 1.2s. Image size: 42 bytes.", at: at},
			loco.ReplicaStarting, "Successfully pulled image in 1.2s. Image size: 42 bytes."},
		{"failed ignored", reasonContainerCreating, &pullEvent{reason: pullReasonFailed, at: at}, loco.ReplicaStarting, ""},
		{"failed container", reasonErrImagePull,
			&pullEvent{reason: pullReasonFailed, at: at},
			loco.ReplicaFailing, "ErrImagePull: "},
		{"pull cannot hide failure", reasonImagePullBackOff,
			&pullEvent{reason: pullReasonPulling, at: at},
			loco.ReplicaFailing, "ImagePullBackOff: "},
		{"clipped pulled detail", reasonPodInitializing,
			&pullEvent{reason: pullReasonPulled, detail: strings.Repeat("x", 600), at: at},
			loco.ReplicaStarting, strings.Repeat("x", replicaDetailBytes)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionTrue}},
				ContainerStatuses: []corev1.ContainerStatus{
					{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: tt.waiting}}},
				},
			}}
			got := replicaState(pod, tt.event, time.Minute, now)
			if got.State != tt.want || got.Detail != tt.detail {
				t.Fatalf("replica = %+v", got)
			}
			if got.State == loco.ReplicaPulling && !got.StateSince.Equal(&at) {
				t.Fatalf("state since = %v, want %v", got.StateSince, at)
			}
			later := replicaState(pod, tt.event, time.Minute, now.Add(time.Second))
			if got.Detail != later.Detail {
				t.Fatalf("detail changed: %q -> %q", got.Detail, later.Detail)
			}
		})
	}
}
