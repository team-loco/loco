package apps

import (
	"context"
	"fmt"
	"testing"
	"time"

	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/tests/e2e/platform"
	corev1 "k8s.io/api/core/v1"
)

func TestClusterCapacityReachesOperators(t *testing.T) {
	t.Parallel()
	var nodes corev1.NodeList
	p.MustGet(t, &nodes, "nodes")
	var cpu, memory int64
	var pods int32
	for _, node := range nodes.Items {
		cpu += node.Status.Allocatable.Cpu().MilliValue()
		memory += node.Status.Allocatable.Memory().Value()
		pods += int32(node.Status.Allocatable.Pods().Value())
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if err := platform.Poll(ctx, func(ctx context.Context) error {
		response, err := p.Admin.ClusterAdmin.ListClusters(ctx, &clusterv1.ListClustersRequest{})
		if err != nil {
			return err
		}
		for _, cluster := range response.GetClusters() {
			if cluster.GetId() != p.ClusterID {
				continue
			}
			capacity := cluster.GetCapacity()
			if capacity == nil || capacity.GetStale() {
				return fmt.Errorf("capacity sample is not fresh: %v", capacity)
			}
			if capacity.GetCpuMillicoresAllocatable() != cpu || capacity.GetMemoryBytesAllocatable() != memory ||
				capacity.GetPodsAllocatable() != pods {
				return fmt.Errorf(
					"capacity %v differs from real nodes: CPU %d memory %d pods %d",
					capacity,
					cpu,
					memory,
					pods,
				)
			}
			if capacity.GetPodsAssigned() < capacity.GetPodsRunning() || capacity.GetCpuMillicoresRequested() <= 0 ||
				capacity.GetMemoryBytesRequested() <= 0 {
				return fmt.Errorf("capacity reservation sample incomplete: %v", capacity)
			}
			_, err = p.Admin.ClusterAdmin.GetClusterOperations(
				ctx,
				&clusterv1.GetClusterOperationsRequest{ClusterId: cluster.GetId()},
			)
			return err
		}
		return fmt.Errorf("cluster %s is not listed", p.ClusterID)
	}); err != nil {
		t.Fatal(err)
	}
}
