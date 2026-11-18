package apps

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

// TestReplicas follows the replicas the API lists under a region: two serving pods, a deleted pod
// replaced by a new one, and a crashing version whose restarts and last exit the API reports, the
// same as `loco resource status -o json`.
func TestReplicas(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-replicas"
	dir := t.TempDir()
	service := platform.ImageService(p.PublicImage)
	service.Health = nil
	service.Regions = map[string]locofile.Region{p.Region: twoReplicas()}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")

	serving := waitReplicas(t, w, app, "two ready replicas", func(region *resourcev1.RegionStatus) error {
		ready := readyNames(region.GetReplicas())
		if len(ready) != 2 {
			return fmt.Errorf("ready replicas %v", ready)
		}
		for _, replica := range region.GetReplicas() {
			if replica.GetNode() == "" || replica.GetStartedAt() == nil ||
				replica.GetDeploymentId() != region.GetDeploymentId() {
				return fmt.Errorf("replica %v lacks a node, start time or the region's deployment", replica)
			}
		}
		return nil
	})
	deleted := readyNames(serving.GetReplicas())[0]
	if _, err := p.Kubectl(t.Context(), "-n", w.Namespace, "delete", "pod", deleted, "--wait=false"); err != nil {
		t.Fatal(err)
	}
	waitReplicas(t, w, app, "a new replica to replace "+deleted, func(region *resourcev1.RegionStatus) error {
		ready := readyNames(region.GetReplicas())
		if len(ready) != 2 || slices.Contains(ready, deleted) {
			return fmt.Errorf("ready replicas %v", ready)
		}
		if !slices.ContainsFunc(region.GetReplicas(), func(replica *deploymentv1.Replica) bool {
			return replica.GetName() == deleted && terminated(replica)
		}) {
			return fmt.Errorf("deleted replica %s is not listed as stopped: %v", deleted, region.GetReplicas())
		}
		return nil
	})

	file.Services[app] = withImage(file.Services[app], p.CrashingImage)
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes")
	// The crashing image runs `sh -c 'sleep 5; exit 1'`, which the kubelet records as Error.
	crashed := func(region *resourcev1.RegionStatus) *deploymentv1.Replica {
		for _, replica := range region.GetReplicas() {
			exit := replica.GetLastExit()
			if replica.GetDeploymentId() == region.GetDeploymentId() && replica.GetRestarts() >= 1 &&
				exit.GetExitCode() == 1 && exit.GetReason() == kubeletExitError &&
				exit.GetFinishedAt().AsTime().Unix() > 0 && replica.GetStartedAt() != nil {
				return replica
			}
		}
		return nil
	}
	waitReplicas(
		t,
		w,
		app,
		"a replica of the crashing version to report its exit",
		func(region *resourcev1.RegionStatus) error {
			if crashed(region) == nil {
				return fmt.Errorf("no replica of deployment %s reports a restart and exit 1: %v",
					region.GetDeploymentId(), region.GetReplicas())
			}
			return nil
		},
	)

	var printed resourcev1.GetResourceResponse
	out := w.MustLoco(t, dir, "resource", "status", app, "-o", "json").Stdout
	if err := protojson.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatalf("decode loco resource status -o json: %v\n%s", err, out)
	}
	found := false
	for _, environment := range printed.GetResource().GetEnvironmentStatuses() {
		for _, region := range environment.GetRegions() {
			found = found || crashed(region) != nil
		}
	}
	if !found {
		t.Errorf("loco resource status -o json lists no crashed replica with its exit:\n%s", out)
	}
}

// kubeletExitError is the reason the kubelet records for a container that exits non-zero.
const kubeletExitError = "Error"

func waitReplicas(
	t *testing.T,
	w *platform.Workspace,
	app, what string,
	check func(*resourcev1.RegionStatus) error,
) *resourcev1.RegionStatus {
	t.Helper()
	var region *resourcev1.RegionStatus
	platform.Eventually(t, statusTimeout, what, func(ctx context.Context) error {
		current, err := statusOf(ctx, w, app)
		if err != nil {
			return err
		}
		region = current.region
		return check(region)
	})
	return region
}

func readyNames(replicas []*deploymentv1.Replica) []string {
	var names []string
	for _, replica := range replicas {
		if replica.GetReady() && replica.GetState() == string(v1alpha1.ReplicaServing) {
			names = append(names, replica.GetName())
		}
	}
	return names
}

func terminated(replica *deploymentv1.Replica) bool {
	return replica.GetState() == string(v1alpha1.ReplicaStopped) || replica.GetState() == string(v1alpha1.ReplicaKilled)
}

// twoReplicas is a small region running exactly two replicas.
func twoReplicas() locofile.Region {
	return locofile.Region{
		CPU:      "100m",
		Memory:   "128Mi",
		Replicas: &locofile.Replicas{Min: new(int32(2)), Max: new(int32(2))},
	}
}
