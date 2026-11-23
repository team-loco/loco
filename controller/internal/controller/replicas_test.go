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

var replicaTestNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func TestReplicaState(t *testing.T) {
	now := replicaTestNow
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
			if got := replicaState(p, nil, now); got.State != tt.want {
				t.Fatalf("state=%s want %s", got.State, tt.want)
			}
		})
	}
	p := base.DeepCopy()
	p.Status.Conditions = p.Status.Conditions[1:]
	if replicaState(p, nil, now).State != loco.ReplicaScheduling {
		t.Fatal("scheduling must precede serving")
	}
	p.Status.ContainerStatuses = []corev1.ContainerStatus{
		{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff}}},
	}
	if replicaState(p, nil, now).State != loco.ReplicaFailing {
		t.Fatal("failure must precede scheduling")
	}
	p = base.DeepCopy()
	p.Status.Conditions[1].Status = corev1.ConditionUnknown
	if replicaState(p, nil, now).State != loco.ReplicaUnknown {
		t.Fatal("unknown readiness")
	}
	for _, tt := range []struct {
		reason, message, want string
	}{
		{"", "", "Pending"},
		{"Preempted", "", "Pending: Preempted"},
		{"", "waiting for a volume", "Pending: waiting for a volume"},
		{"Preempted", "by a higher priority pod", "Pending: Preempted by a higher priority pod"},
	} {
		p = base.DeepCopy()
		p.Status.Conditions = p.Status.Conditions[:1]
		p.Status.Phase, p.Status.Reason, p.Status.Message = corev1.PodPending, tt.reason, tt.message
		if got := replicaState(p, nil, now).Detail; got != tt.want {
			t.Fatalf("detail = %q, want %q", got, tt.want)
		}
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
			got := replicaState(p, nil, now)
			if got.State != tt.want || got.LastExit == nil || got.LastExit.ExitCode != 137 || got.StateDeadline != nil {
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
		got := replicaState(p, nil, now)
		if got.State != loco.ReplicaStopping || got.StateDeadline == nil || !got.StateDeadline.Time.Equal(want) {
			t.Fatalf("stopping=%+v want deadline %s", got, want)
		}
	}
}
func TestRolloutAvailable(t *testing.T) {
	app := &loco.Application{
		Annotations: map[string]string{loco.KeyPlacementRevision: "2"},
		Spec:        loco.ApplicationSpec{ServiceSpec: &loco.ServiceSpec{Deployment: &loco.ServiceDeploymentSpec{}}},
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
	app.Status.ObservedPlacementRevision = 2
	dep.Status.WithAvailableReplicas(1).WithReadyReplicas(2)
	if rolloutAvailable(dep, app) {
		t.Fatal("worker accepted a ready replica that is not yet available")
	}
	app.Spec.ServiceSpec.Deployment.Port = new(int32(8000))
	if !rolloutAvailable(dep, app) {
		t.Fatal("service waited for a ready replica to become available")
	}
	if rolloutSettled(dep, app) {
		t.Fatal("settled with a replica not yet available")
	}
	dep.Status.WithReplicas(3).WithReadyReplicas(3)
	if rolloutAvailable(dep, app) {
		t.Fatal("service accepted an old counted replica")
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
	got := replicaState(pod, nil, time.Now())
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
		{"waiting to restart", corev1.ContainerStatus{RestartCount: 1,
			State: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff}},
			LastTerminationState: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}}, loco.ReplicaFailing},
		{"ready after a restart", corev1.ContainerStatus{Ready: true, RestartCount: 1,
			State: corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(now.Add(-10 * time.Second))}},
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
			if got := replicaState(pod, nil, now); got.State != tt.want {
				t.Fatalf("state=%s want %s", got.State, tt.want)
			}
		})
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
		a := replicaState(pod, nil, now.Add(offset))
		b := replicaState(pod, nil, now.Add(offset+time.Second))
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
		event   *podEvent
		want    loco.ReplicaState
		detail  string
	}{
		{"pulling", reasonContainerCreating, &podEvent{pull: pullReasonPulling, pulledAt: at}, loco.ReplicaPulling, ""},
		{"initializing", reasonPodInitializing, &podEvent{pull: pullReasonPulling, pulledAt: at}, loco.ReplicaPulling, ""},
		{"missing", reasonContainerCreating, nil, loco.ReplicaStarting, ""},
		{"pulled", reasonContainerCreating, &podEvent{pull: pullReasonPulled, pulledAt: at}, loco.ReplicaStarting, ""},
		{"failed ignored", reasonContainerCreating,
			&podEvent{pull: pullReasonFailed, pulledAt: at}, loco.ReplicaStarting, ""},
		{"failed container", reasonErrImagePull,
			&podEvent{pull: pullReasonFailed, pulledAt: at},
			loco.ReplicaFailing, "ErrImagePull: "},
		{"pull cannot hide failure", reasonImagePullBackOff,
			&podEvent{pull: pullReasonPulling, pulledAt: at},
			loco.ReplicaFailing, "ImagePullBackOff: "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionTrue}},
				ContainerStatuses: []corev1.ContainerStatus{
					{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: tt.waiting}}},
				},
			}}
			got := replicaState(pod, tt.event, now)
			if got.State != tt.want || got.Detail != tt.detail {
				t.Fatalf("replica = %+v", got)
			}
			if got.State == loco.ReplicaPulling && !got.StateSince.Equal(&at) {
				t.Fatalf("state since = %v, want %v", got.StateSince, at)
			}
			later := replicaState(pod, tt.event, now.Add(time.Second))
			if got.Detail != later.Detail {
				t.Fatalf("detail changed: %q -> %q", got.Detail, later.Detail)
			}
		})
	}
}

func TestReplicaDeploymentIdentityAndReadiness(t *testing.T) {
	const deploymentID = "019ab668-1111-7000-8000-000011112222"
	pod := corev1.Pod{
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Env: []corev1.EnvVar{{Name: envDeploymentID, Value: deploymentID}},
		}}},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			ContainerStatuses: []corev1.ContainerStatus{{
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff}},
			}},
		},
	}
	now := time.Now()
	got := replicaState(&pod, nil, now)
	if got.DeploymentID != deploymentID || !got.Ready || got.State != loco.ReplicaFailing {
		t.Fatalf("ready recovering replica = %+v", got)
	}
	pod.DeletionTimestamp = new(metav1.NewTime(now))
	if got := replicaState(&pod, nil, now); got.Ready {
		t.Fatalf("terminating Pod reported ready: %+v", got)
	}
	pod.DeletionTimestamp = nil
	pod.Status.Conditions[0].Status = corev1.ConditionFalse
	if got := replicaState(&pod, nil, now); got.Ready {
		t.Fatalf("unready Pod reported ready: %+v", got)
	}
}

const exitReasonError = "Error"

func TestReplicaLastExit(t *testing.T) {
	now := replicaTestNow
	older := metav1.NewTime(now.Add(-time.Minute))
	newer := metav1.NewTime(now.Add(-time.Second))
	oom := &corev1.ContainerStateTerminated{Reason: reasonOOMKilled, ExitCode: 137, FinishedAt: older}
	crash := &corev1.ContainerStateTerminated{Reason: exitReasonError, ExitCode: 1, FinishedAt: newer}
	for _, tt := range []struct {
		name     string
		statuses []corev1.ContainerStatus
		want     *loco.ReplicaExit
	}{
		{"never exited", []corev1.ContainerStatus{{
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}}, nil},
		{"previous run", []corev1.ContainerStatus{{
			RestartCount:         1,
			State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			LastTerminationState: corev1.ContainerState{Terminated: oom},
		}}, &loco.ReplicaExit{Reason: reasonOOMKilled, ExitCode: 137, FinishedAt: older}},
		{"current exit is newer", []corev1.ContainerStatus{{
			RestartCount:         1,
			State:                corev1.ContainerState{Terminated: crash},
			LastTerminationState: corev1.ContainerState{Terminated: oom},
		}}, &loco.ReplicaExit{Reason: exitReasonError, ExitCode: 1, FinishedAt: newer}},
		{"newest container exit", []corev1.ContainerStatus{
			{LastTerminationState: corev1.ContainerState{Terminated: oom}},
			{LastTerminationState: corev1.ContainerState{Terminated: crash}},
		}, &loco.ReplicaExit{Reason: exitReasonError, ExitCode: 1, FinishedAt: newer}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: tt.statuses}}
			got := replicaState(pod, nil, now).LastExit
			if tt.want == nil {
				if got != nil {
					t.Fatalf("last exit = %+v, want none", got)
				}
				return
			}
			if got == nil || got.Reason != tt.want.Reason || got.ExitCode != tt.want.ExitCode ||
				!got.FinishedAt.Equal(&tt.want.FinishedAt) {
				t.Fatalf("last exit = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReplicaStartedAt(t *testing.T) {
	now := replicaTestNow
	created := metav1.NewTime(now.Add(-time.Hour))
	started := metav1.NewTime(now.Add(-time.Minute))
	pod := &corev1.Pod{CreationTimestamp: created}
	if got := replicaState(pod, nil, now).StartedAt; got == nil || !got.Equal(&created) {
		t.Fatalf("unstarted pod started at %v, want creation time %v", got, created)
	}
	pod.Status.StartTime = &started
	if got := replicaState(pod, nil, now).StartedAt; got == nil || !got.Equal(&started) {
		t.Fatalf("started pod started at %v, want %v", got, started)
	}
}

func TestShutdownDetailNamesTheExitRelativeToTheDrain(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		name   string
		offset time.Duration
		want   string
	}{
		{"before the drain ends", -10 * time.Second, "exit 2 · during the drain"},
		{"after the drain ends", 3 * time.Second, "exit 2 · 3 s after the drain"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &corev1.Pod{}
			deadline := metav1.NewTime(now.Add(5 * time.Second))
			p.DeletionTimestamp = &deadline
			p.DeletionGracePeriodSeconds = new(int64(35))
			p.Spec.Containers = []corev1.Container{{
				Lifecycle: &corev1.Lifecycle{
					PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: 30}},
				},
			}}
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					ExitCode:   2,
					FinishedAt: metav1.NewTime(now.Add(tt.offset)),
				}},
			}}
			got := replicaState(p, nil, now)
			if got.State != loco.ReplicaStopped || got.Detail != tt.want {
				t.Fatalf("state=%s detail=%q, want Stopped %q", got.State, got.Detail, tt.want)
			}
		})
	}
}

func TestReplicaDetailOmitsReadyConditionText(t *testing.T) {
	const unready = "containers with unready status: [web]"
	running := corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(replicaTestNow)}}
	for _, tt := range []struct {
		name      string
		ready     corev1.ConditionStatus
		container bool
		want      string
	}{
		{"container not ready", corev1.ConditionFalse, false, detailWaitingForReadiness},
		{"pod not ready", corev1.ConditionFalse, true, detailWaitingForReadiness},
		{"pod ready", corev1.ConditionTrue, true, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
					{Type: corev1.PodReady, Status: tt.ready, Message: unready},
				},
				ContainerStatuses: []corev1.ContainerStatus{{Ready: tt.container, State: running}},
			}}
			if got := replicaState(pod, nil, replicaTestNow).Detail; got != tt.want {
				t.Fatalf("detail = %q, want %q", got, tt.want)
			}
		})
	}
}
