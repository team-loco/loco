package main

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/team-loco/loco/agent/pkg/egress"
	"github.com/team-loco/loco/agent/pkg/usage"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testUsageInterval = time.Hour
	testUsageWait     = 2 * time.Second
	testUsageID       = "0198c3c4-0000-7000-8000-00000000000a"
)

func endedPod() *corev1.Pod {
	started := metav1.NewTime(time.Now().Add(-time.Minute))
	return &corev1.Pod{
		Name: "app",
		UID:  testUsageID,
		Labels: map[string]string{
			locolabels.LabelOrganizationID: testUsageID,
			locolabels.LabelWorkspaceID:    testUsageID,
			locolabels.LabelResourceID:     testUsageID,
			locolabels.LabelEnvironmentID:  testUsageID,
		},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: started}},
		}}},
	}
}

type noApplications struct{}

func (noApplications) Applications(context.Context) ([]locov1alpha1.Application, error) {
	return nil, nil
}

func TestReportUsageReportsAtOnceAndWhenARunEnds(t *testing.T) {
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	meter := usage.New(reader, time.Now)
	gateway := egress.New(kubefake.NewClientset(), egress.Config{}, noApplications{}, nil)
	a := &Agent{cfg: &Config{UsageInterval: testUsageInterval}, usage: meter, egress: gateway}
	ctx, cancel := context.WithCancel(t.Context())
	sent := make(chan *agentv1.SyncRequest)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		a.reportUsage(ctx, func(msg *agentv1.SyncRequest) {
			select {
			case sent <- msg:
			case <-ctx.Done():
			}
		})
	}()

	receive := func(when string) *agentv1.UsageReport {
		t.Helper()
		select {
		case msg := <-sent:
			if !msg.GetUsage().GetEgressComplete() {
				t.Fatalf("sent %v %s, want a usage report that read every gateway proxy", msg, when)
			}
			return msg.GetUsage()
		case <-time.After(testUsageWait):
			t.Fatalf("no usage report %s", when)
		}
		return nil
	}
	receive("at once")
	meter.Forget(endedPod())
	if report := receive("after a run ended"); len(report.GetRuns()) != 1 {
		t.Fatalf("runs = %v, want the ended run", report.GetRuns())
	}

	cancel()
	<-stopped
}
