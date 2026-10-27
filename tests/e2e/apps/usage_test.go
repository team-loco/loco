package apps

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	corev1 "k8s.io/api/core/v1"

	usagev1 "github.com/team-loco/loco/gen/go/loco/usage/v1"
	"github.com/team-loco/loco/internal/locofile"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	usageTimeout     = 3 * time.Minute
	usagePageSize    = 1000
	usageMillicores  = 100
	usageMemoryBytes = 128 << 20
)

func TestWorkloadRunsMeterApplicationPods(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-usage"
	file := locofile.File{
		Partial:  app,
		Services: map[string]locofile.Service{app: platform.ImageService(p.PublicImage)},
	}
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	resourceID := w.ResourceID(t, app)
	deployedAt := time.Now()

	first := runningPod(t, w, resourceID, "")
	waitForRun(t, "the first pod's run", first, func(run *usagev1.WorkloadRun) error {
		return runningAllocation(run, resourceID)
	})
	platform.Eventually(t, usageTimeout, "metered_through to pass the deploy", func(ctx context.Context) error {
		page, err := p.Admin.Usage.ListWorkloadRuns(ctx, &usagev1.ListWorkloadRunsRequest{})
		if err != nil {
			return err
		}
		if through := page.GetMeteredThrough(); through == nil || !through.AsTime().After(deployedAt) {
			return fmt.Errorf("metered through %v", through.AsTime())
		}
		return nil
	})

	p.MustKubectl(t, "-n", w.Namespace, "delete", "pod", "--wait=true", "-l", locolabels.LabelResourceID+"="+resourceID)
	waitForRun(t, "the deleted pod's run to end", first, func(run *usagev1.WorkloadRun) error {
		if run.GetEndedAt() == nil {
			return errors.New("run has not ended")
		}
		return nil
	})
	replacement := runningPod(t, w, resourceID, first)
	waitForRun(t, "the replacement pod's run", replacement, func(run *usagev1.WorkloadRun) error {
		return runningAllocation(run, resourceID)
	})

	_, err := p.User.Usage.ListWorkloadRuns(t.Context(), &usagev1.ListWorkloadRunsRequest{})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("a workspace owner listed workload runs: %v", err)
	}
}

func runningAllocation(run *usagev1.WorkloadRun, resourceID string) error {
	if run.GetCpuMillicores() != usageMillicores || run.GetMemoryBytes() != usageMemoryBytes ||
		run.GetResourceId() != resourceID || run.GetClusterId() != p.ClusterID || run.GetEndedAt() != nil {
		return fmt.Errorf("run = %v", run)
	}
	return nil
}

// runningPod returns the UID of the resource's running pod other than previous.
func runningPod(t *testing.T, w *platform.Workspace, resourceID, previous string) string {
	t.Helper()
	var uid string
	platform.Eventually(t, usageTimeout, "a running pod", func(ctx context.Context) error {
		var pods corev1.PodList
		selector := locolabels.LabelResourceID + "=" + resourceID
		if err := p.Get(ctx, &pods, "-n", w.Namespace, "pods", "-l", selector); err != nil {
			return err
		}
		for i := range pods.Items {
			pod := &pods.Items[i]
			if pod.Status.Phase == corev1.PodRunning && pod.DeletionTimestamp == nil && string(pod.UID) != previous {
				uid = string(pod.UID)
				return nil
			}
		}
		return errors.New("no running pod")
	})
	return uid
}

// waitForRun reads the whole feed until the latest version of the pod's run passes check.
func waitForRun(t *testing.T, what, podUID string, check func(*usagev1.WorkloadRun) error) {
	t.Helper()
	platform.Eventually(t, usageTimeout, what, func(ctx context.Context) error {
		var run *usagev1.WorkloadRun
		cursor := ""
		for {
			page, err := p.Admin.Usage.ListWorkloadRuns(
				ctx,
				&usagev1.ListWorkloadRunsRequest{Cursor: cursor, PageSize: usagePageSize},
			)
			if err != nil {
				return err
			}
			for _, row := range page.GetRuns() {
				if row.GetId() == podUID {
					run = row
				}
			}
			if page.GetNextCursor() == cursor {
				break
			}
			cursor = page.GetNextCursor()
		}
		if run == nil {
			return fmt.Errorf("no run for pod %s", podUID)
		}
		return check(run)
	})
}

func TestEgressUsageMetersGatewayTraffic(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-egress"
	host := w.Host(app)
	file := locofile.File{
		Partial:  app,
		Services: map[string]locofile.Service{app: platform.ImageService(p.PublicImage, host)},
	}
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	resourceID := w.ResourceID(t, app)
	gateway, err := p.GatewayService(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const requester = "e2e-egress-requester"
	p.StartPod(t, platform.ProbePod{Namespace: p.Namespace, Name: requester, Command: []string{
		"sh",
		"-c",
		fmt.Sprintf(
			`while :; do wget -q -T 2 -O /dev/null --header "Host: %s" "http://%s.%s.svc.cluster.local/"; sleep 1; done`,
			host,
			gateway,
			p.Namespace,
		),
	}})
	p.DeleteOnCleanup(t, "-n", p.Namespace, "pod", requester)

	var first int64
	platform.Eventually(t, usageTimeout, "egress for the service", func(ctx context.Context) error {
		var countErr error
		first, countErr = egressBytes(ctx, resourceID)
		if countErr != nil {
			return countErr
		}
		if first == 0 {
			return errors.New("no egress yet")
		}
		return nil
	})
	platform.Eventually(t, usageTimeout, "egress to grow", func(ctx context.Context) error {
		bytes, countErr := egressBytes(ctx, resourceID)
		if countErr != nil {
			return countErr
		}
		if bytes <= first {
			return fmt.Errorf("egress %d B, want more than %d B", bytes, first)
		}
		return nil
	})
	page, err := p.Admin.Usage.ListEgressUsage(t.Context(), &usagev1.ListEgressUsageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if page.GetMeteredThrough() == nil {
		t.Fatal("the egress feed has no metered_through")
	}
	_, err = p.User.Usage.ListEgressUsage(t.Context(), &usagev1.ListEgressUsageRequest{})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("a workspace owner listed egress usage: %v", err)
	}
}

// egressBytes sums the latest version of every hourly egress row of the resource.
func egressBytes(ctx context.Context, resourceID string) (int64, error) {
	latest := map[string]int64{}
	cursor := ""
	for {
		page, err := p.Admin.Usage.ListEgressUsage(
			ctx,
			&usagev1.ListEgressUsageRequest{Cursor: cursor, PageSize: usagePageSize},
		)
		if err != nil {
			return 0, err
		}
		for _, row := range page.GetUsage() {
			if row.GetResourceId() == resourceID && row.GetClusterId() == p.ClusterID {
				latest[row.GetEnvironmentId()+row.GetHourStart().AsTime().String()] = row.GetBytes()
			}
		}
		if page.GetNextCursor() == cursor {
			break
		}
		cursor = page.GetNextCursor()
	}
	var total int64
	for _, bytes := range latest {
		total += bytes
	}
	return total, nil
}
