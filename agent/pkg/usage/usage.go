package usage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
)

var errCacheNotSynced = errors.New("pod cache did not sync")

var ownerLabels = []string{
	locolabels.LabelOrganizationID,
	locolabels.LabelWorkspaceID,
	locolabels.LabelResourceID,
	locolabels.LabelEnvironmentID,
}

type endedRun struct {
	run        *agentv1.PodRun
	reportedIn time.Time
}

type Meter struct {
	reader client.Reader
	now    func() time.Time

	mu    sync.Mutex
	ended map[types.UID]*endedRun
	wake  chan struct{}
}

func ApplicationPods() labels.Selector {
	return labels.SelectorFromSet(labels.Set{
		locolabels.LabelManagedBy: locolabels.ManagedByController,
		locolabels.LabelComponent: locolabels.ComponentApplication,
	})
}

func Start(ctx context.Context, cfg *rest.Config) (*Meter, error) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add core types to scheme: %w", err)
	}
	selector := ApplicationPods()
	c, err := cache.New(cfg, cache.Options{
		Scheme: scheme,
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Pod{}: {Label: selector},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create pod cache: %w", err)
	}
	m := New(c, time.Now)
	informer, err := c.GetInformer(ctx, &corev1.Pod{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod informer: %w", err)
	}
	_, err = informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		UpdateFunc: m.observeUpdate,
		DeleteFunc: m.Forget,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to watch pods: %w", err)
	}
	go func() {
		if startErr := c.Start(ctx); startErr != nil {
			slog.ErrorContext(ctx, "pod cache stopped", "error", startErr)
		}
	}()
	if !c.WaitForCacheSync(ctx) {
		return nil, errCacheNotSynced
	}
	return m, nil
}

func New(reader client.Reader, now func() time.Time) *Meter {
	return &Meter{
		reader: reader,
		now:    now,
		ended:  make(map[types.UID]*endedRun),
		wake:   make(chan struct{}, 1),
	}
}

func (m *Meter) Wake() <-chan struct{} {
	return m.wake
}

func (m *Meter) observeUpdate(_, obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok || !terminal(pod) {
		return
	}
	m.end(pod)
}

func (m *Meter) Forget(obj any) {
	if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	m.end(pod)
}

func (m *Meter) end(pod *corev1.Pod) {
	now := m.now()
	run, ok := Ended(pod, now)
	if !ok {
		return
	}
	m.mu.Lock()
	if _, buffered := m.ended[pod.UID]; !buffered {
		m.ended[pod.UID] = &endedRun{run: run}
	}
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Meter) Report(ctx context.Context) (*agentv1.UsageReport, error) {
	var pods corev1.PodList
	selector := ApplicationPods()
	if err := m.reader.List(ctx, &pods, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("failed to list application pods: %w", err)
	}
	now := m.now().Round(0)
	runs := make(map[types.UID]*agentv1.PodRun, len(pods.Items))
	for i := range pods.Items {
		run, ok := Run(&pods.Items[i], now)
		if ok {
			runs[pods.Items[i].UID] = run
		}
	}
	m.mu.Lock()
	for uid, buffered := range m.ended {
		runs[uid] = buffered.run
		buffered.reportedIn = now
	}
	m.mu.Unlock()
	report := &agentv1.UsageReport{
		ObservedAt: timestamppb.New(now),
		Runs:       make([]*agentv1.PodRun, 0, len(runs)),
	}
	for _, run := range runs {
		report.Runs = append(report.Runs, run)
	}
	return report, nil
}

func (m *Meter) Recorded(observedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for uid, buffered := range m.ended {
		if !buffered.reportedIn.IsZero() && !buffered.reportedIn.After(observedAt) {
			delete(m.ended, uid)
		}
	}
}

func Run(pod *corev1.Pod, now time.Time) (*agentv1.PodRun, bool) {
	for _, label := range ownerLabels {
		if pod.Labels[label] == "" {
			return nil, false
		}
	}
	started, ok := startedAt(pod)
	if !ok {
		return nil, false
	}
	run := &agentv1.PodRun{
		PodUid:         string(pod.UID),
		OrganizationId: pod.Labels[locolabels.LabelOrganizationID],
		WorkspaceId:    pod.Labels[locolabels.LabelWorkspaceID],
		ResourceId:     pod.Labels[locolabels.LabelResourceID],
		EnvironmentId:  pod.Labels[locolabels.LabelEnvironmentID],
		StartedAt:      timestamppb.New(started),
	}
	for i := range pod.Spec.Containers {
		requests := pod.Spec.Containers[i].Resources.Requests
		run.CpuMillicores += requests.Cpu().MilliValue()
		run.MemoryBytes += requests.Memory().Value()
	}
	if terminal(pod) {
		finished := finishedAt(pod, now)
		run.EndedAt = timestamppb.New(finished)
	}
	return run, true
}

func Ended(pod *corev1.Pod, now time.Time) (*agentv1.PodRun, bool) {
	run, ok := Run(pod, now)
	if !ok {
		return nil, false
	}
	if run.GetEndedAt() == nil {
		finished := finishedAt(pod, now)
		run.EndedAt = timestamppb.New(finished)
	}
	return run, true
}

func terminal(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

func startedAt(pod *corev1.Pod) (time.Time, bool) {
	var earliest time.Time
	consider := func(at time.Time) {
		if !at.IsZero() && (earliest.IsZero() || at.Before(earliest)) {
			earliest = at
		}
	}
	for i := range pod.Status.ContainerStatuses {
		status := &pod.Status.ContainerStatuses[i]
		if status.State.Running != nil {
			consider(status.State.Running.StartedAt.Time)
		}
		if status.State.Terminated != nil {
			consider(status.State.Terminated.StartedAt.Time)
		}
		if status.LastTerminationState.Terminated != nil {
			consider(status.LastTerminationState.Terminated.StartedAt.Time)
		}
	}
	return earliest, !earliest.IsZero()
}

func finishedAt(pod *corev1.Pod, now time.Time) time.Time {
	var latest time.Time
	for i := range pod.Status.ContainerStatuses {
		terminated := pod.Status.ContainerStatuses[i].State.Terminated
		if terminated == nil || terminated.FinishedAt.IsZero() {
			return now
		}
		if terminated.FinishedAt.After(latest) {
			latest = terminated.FinishedAt.Time
		}
	}
	if latest.IsZero() {
		return now
	}
	return latest
}
