package usage

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
)

const (
	testNamespace     = "ws-0198c3c4-0000-7000-8000-000000000001"
	testOrgID         = "0198c3c4-0000-7000-8000-00000000000a"
	testWorkspaceID   = "0198c3c4-0000-7000-8000-000000000001"
	testResourceID    = "0198c3c4-0000-7000-8000-00000000000b"
	testEnvironmentID = "0198c3c4-0000-7000-8000-00000000000c"
	testPodUID        = "0198c3c4-0000-7000-8000-0000000000aa"
	testOtherPodUID   = "0198c3c4-0000-7000-8000-0000000000bb"
	testAppCPU        = "500m"
	testAppMemory     = "512Mi"
	testSidecarCPU    = "250m"
	testSidecarMemory = "128Mi"
	testAppMillicores = 750
	testAppBytes      = 512<<20 + 128<<20
)

var (
	testStart = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	testNow   = testStart.Add(time.Hour)
)

func requests(cpu, memory string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{Requests: corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(cpu),
		corev1.ResourceMemory: resource.MustParse(memory),
	}}
}

func running(at time.Time) corev1.ContainerState {
	return corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(at)}}
}

func terminated(started, finished time.Time) corev1.ContainerState {
	return corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
		StartedAt:  metav1.NewTime(started),
		FinishedAt: metav1.NewTime(finished),
	}}
}

func appPod(uid string, phase corev1.PodPhase, states ...corev1.ContainerState) *corev1.Pod {
	pod := &corev1.Pod{
		Name:      "app-" + uid,
		Namespace: testNamespace,
		UID:       types.UID(uid),
		Labels: map[string]string{
			locolabels.LabelManagedBy:      locolabels.ManagedByController,
			locolabels.LabelComponent:      locolabels.ComponentApplication,
			locolabels.LabelOrganizationID: testOrgID,
			locolabels.LabelWorkspaceID:    testWorkspaceID,
			locolabels.LabelResourceID:     testResourceID,
			locolabels.LabelEnvironmentID:  testEnvironmentID,
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Resources: requests(testAppCPU, testAppMemory)},
			{Name: "sidecar", Resources: requests(testSidecarCPU, testSidecarMemory)},
		}},
		Status: corev1.PodStatus{Phase: phase},
	}
	for i, state := range states {
		name := pod.Spec.Containers[i].Name
		status := corev1.ContainerStatus{Name: name, State: state}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, status)
	}
	return pod
}

func TestRunReportsTheSummedRequestsAndOwnerOfARunningPod(t *testing.T) {
	later := testStart.Add(time.Second)
	pod := appPod(testPodUID, corev1.PodRunning, running(later), running(testStart))
	run, ok := Run(pod, testNow)
	if !ok {
		t.Fatal("a running application pod was not reported")
	}
	if run.GetCpuMillicores() != testAppMillicores || run.GetMemoryBytes() != testAppBytes {
		t.Errorf("allocation = %dm %dB, want %dm %dB",
			run.GetCpuMillicores(), run.GetMemoryBytes(), testAppMillicores, testAppBytes)
	}
	if !run.GetStartedAt().AsTime().Equal(testStart) {
		t.Errorf("started at %s, want the first container start %s", run.GetStartedAt().AsTime(), testStart)
	}
	if run.GetEndedAt() != nil {
		t.Errorf("a running pod ended at %s", run.GetEndedAt().AsTime())
	}
	owner := []string{run.GetOrganizationId(), run.GetWorkspaceId(), run.GetResourceId(), run.GetEnvironmentId()}
	want := []string{testOrgID, testWorkspaceID, testResourceID, testEnvironmentID}
	for i := range want {
		if owner[i] != want[i] {
			t.Errorf("owner = %v, want %v", owner, want)
			break
		}
	}
	if run.GetPodUid() != testPodUID {
		t.Errorf("pod uid = %s, want %s", run.GetPodUid(), testPodUID)
	}
}

func TestRunSkipsAPodThatNeverRan(t *testing.T) {
	waiting := corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}
	pod := appPod(testPodUID, corev1.PodPending, waiting, waiting)
	if run, ok := Run(pod, testNow); ok {
		t.Fatalf("a pending pod was reported as %v", run)
	}
}

func TestRunSkipsAPodWithoutItsOwnerLabels(t *testing.T) {
	for _, label := range ownerLabels {
		t.Run(label, func(t *testing.T) {
			pod := appPod(testPodUID, corev1.PodRunning, running(testStart), running(testStart))
			delete(pod.Labels, label)
			if run, ok := Run(pod, testNow); ok {
				t.Fatalf("a pod without %s was reported as %v", label, run)
			}
		})
	}
}

func TestRunKeepsTheStartOfAContainerThatRestarted(t *testing.T) {
	restarted := testStart.Add(30 * time.Minute)
	pod := appPod(testPodUID, corev1.PodRunning, running(restarted))
	pod.Status.ContainerStatuses[0].LastTerminationState = terminated(testStart, restarted)
	run, ok := Run(pod, testNow)
	if !ok || !run.GetStartedAt().AsTime().Equal(testStart) {
		t.Fatalf("run = %v, want it to start at the first run %s", run, testStart)
	}
}

func TestRunEndsATerminalPodWhenItsLastContainerFinished(t *testing.T) {
	first := testStart.Add(10 * time.Minute)
	last := testStart.Add(20 * time.Minute)
	pod := appPod(testPodUID, corev1.PodFailed, terminated(testStart, last), terminated(testStart, first))
	run, ok := Run(pod, testNow)
	if !ok || run.GetEndedAt() == nil || !run.GetEndedAt().AsTime().Equal(last) {
		t.Fatalf("run = %v, want it to end at %s", run, last)
	}
}

func TestEndedEndsADeletedPodWhenItWasLastKnownToRun(t *testing.T) {
	finished := testStart.Add(10 * time.Minute)
	stopped := appPod(testPodUID, corev1.PodRunning, terminated(testStart, finished), terminated(testStart, finished))
	run, ok := Ended(stopped, testNow)
	if !ok || !run.GetEndedAt().AsTime().Equal(finished) {
		t.Fatalf("run = %v, want it to end when its containers finished at %s", run, finished)
	}

	lost := appPod(testPodUID, corev1.PodRunning, running(testStart), running(testStart))
	run, ok = Ended(lost, testNow)
	if !ok || !run.GetEndedAt().AsTime().Equal(testNow) {
		t.Fatalf("run = %v, want a pod deleted while running to end when the agent saw it go, %s", run, testNow)
	}
}

type clock struct{ now time.Time }

func (c *clock) read() time.Time { return c.now }

func newTestMeter(t *testing.T, objects ...client.Object) (*Meter, *clock) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	c := &clock{now: testNow}
	return New(reader, c.read), c
}

func reportedRuns(t *testing.T, m *Meter) (map[string]*agentv1.PodRun, time.Time) {
	t.Helper()
	report, err := m.Report(t.Context())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	runs := make(map[string]*agentv1.PodRun, len(report.GetRuns()))
	for _, run := range report.GetRuns() {
		runs[run.GetPodUid()] = run
	}
	return runs, report.GetObservedAt().AsTime()
}

func TestReportListsRunningApplicationPodsOnly(t *testing.T) {
	app := appPod(testPodUID, corev1.PodRunning, running(testStart), running(testStart))
	unmanaged := appPod(testOtherPodUID, corev1.PodRunning, running(testStart), running(testStart))
	unmanaged.Labels[locolabels.LabelComponent] = "build"
	m, _ := newTestMeter(t, app, unmanaged)

	runs, observedAt := reportedRuns(t, m)
	if len(runs) != 1 || runs[testPodUID] == nil {
		t.Fatalf("runs = %v, want only the application pod", runs)
	}
	if !observedAt.Equal(testNow) {
		t.Errorf("observed at %s, want %s", observedAt, testNow)
	}
}

func TestReportResendsAnEndedRunUntilAReportWithItIsRecorded(t *testing.T) {
	m, c := newTestMeter(t)
	pod := appPod(testPodUID, corev1.PodRunning, running(testStart), running(testStart))
	m.Forget(toolscache.DeletedFinalStateUnknown{Obj: pod})
	select {
	case <-m.Wake():
	default:
		t.Fatal("ending a run did not wake the reporter")
	}

	runs, first := reportedRuns(t, m)
	if runs[testPodUID].GetEndedAt() == nil {
		t.Fatalf("runs = %v, want the deleted pod reported as ended", runs)
	}
	m.Recorded(first.Add(-time.Second))
	c.now = c.now.Add(time.Minute)
	runs, second := reportedRuns(t, m)
	if runs[testPodUID] == nil {
		t.Fatal("an ended run was dropped before any report holding it was recorded")
	}

	m.Recorded(first)
	runs, _ = reportedRuns(t, m)
	if runs[testPodUID] == nil {
		t.Fatal("an ended run was dropped by the record of an older report than the last one holding it")
	}
	m.Recorded(second)
	c.now = c.now.Add(time.Minute)
	runs, _ = reportedRuns(t, m)
	if runs[testPodUID] != nil {
		t.Fatalf("runs = %v, want the recorded ended run gone", runs)
	}
}

func TestRecordedKeepsARunThatEndedAfterTheReport(t *testing.T) {
	m, _ := newTestMeter(t)
	_, observedAt := reportedRuns(t, m)
	pod := appPod(testPodUID, corev1.PodRunning, running(testStart), running(testStart))
	m.Forget(pod)
	m.Recorded(observedAt)
	runs, _ := reportedRuns(t, m)
	if runs[testPodUID] == nil {
		t.Fatal("a run that ended after the recorded report was dropped")
	}
}

func TestUpdateEndsAPodOnlyOnceItIsTerminal(t *testing.T) {
	m, _ := newTestMeter(t)
	live := appPod(testPodUID, corev1.PodRunning, running(testStart), running(testStart))
	m.observeUpdate(nil, live)
	if runs, _ := reportedRuns(t, m); len(runs) != 0 {
		t.Fatalf("runs = %v, want nothing buffered for a live pod", runs)
	}
	finished := testStart.Add(time.Minute)
	done := appPod(testPodUID, corev1.PodSucceeded, terminated(testStart, finished), terminated(testStart, finished))
	m.observeUpdate(live, done)
	runs, _ := reportedRuns(t, m)
	if !runs[testPodUID].GetEndedAt().AsTime().Equal(finished) {
		t.Fatalf("runs = %v, want the terminal pod buffered as ended at %s", runs, finished)
	}
}
