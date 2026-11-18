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

	serving := waitReplicas(
		t,
		w,
		app,
		"two ready replicas with distinct names",
		func(replicas []*deploymentv1.Replica) error {
			if names := readyNames(replicas); len(names) != 2 {
				return fmt.Errorf("ready replicas %v", names)
			}
			return nil
		},
	)
	deleted := readyNames(serving)[0]
	if _, err := p.Kubectl(t.Context(), "-n", w.Namespace, "delete", "pod", deleted, "--wait=false"); err != nil {
		t.Fatal(err)
	}
	waitReplicas(t, w, app, "a new replica to replace "+deleted, func(replicas []*deploymentv1.Replica) error {
		names := readyNames(replicas)
		if len(names) != 2 || slices.Contains(names, deleted) {
			return fmt.Errorf("ready replicas %v", names)
		}
		for _, replica := range replicas {
			if replica.GetName() == deleted && !terminated(replica) {
				return fmt.Errorf("deleted replica %s is %s", deleted, replica.GetState())
			}
		}
		return nil
	})

	file.Services[app] = withImage(file.Services[app], p.CrashingImage)
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes")
	crashed := func(replicas []*deploymentv1.Replica) *deploymentv1.Replica {
		for _, replica := range replicas {
			exit := replica.GetLastExit()
			if replica.GetRestarts() >= 1 && exit.GetExitCode() == 1 && exit.GetReason() != "" &&
				exit.GetFinishedAt().AsTime().Unix() > 0 && replica.GetStartedAt() != nil {
				return replica
			}
		}
		return nil
	}
	replicas := waitReplicas(
		t,
		w,
		app,
		"a crashing replica to report its exit",
		func(replicas []*deploymentv1.Replica) error {
			if crashed(replicas) == nil {
				return fmt.Errorf("no replica reports a restart and exit 1: %v", replicas)
			}
			return nil
		},
	)
	want := crashed(replicas)

	var printed resourcev1.GetResourceResponse
	out := w.MustLoco(t, dir, "resource", "status", app, "-o", "json").Stdout
	if err := protojson.Unmarshal([]byte(out), &printed); err != nil {
		t.Fatalf("decode loco resource status -o json: %v\n%s", err, out)
	}
	found := false
	for _, environment := range printed.GetResource().GetEnvironmentStatuses() {
		for _, region := range environment.GetRegions() {
			found = found || slices.ContainsFunc(region.GetReplicas(), func(replica *deploymentv1.Replica) bool {
				return replica.GetName() == want.GetName()
			})
		}
	}
	if !found {
		t.Errorf("loco resource status -o json lists no replica %s:\n%s", want.GetName(), out)
	}
}

func waitReplicas(
	t *testing.T,
	w *platform.Workspace,
	app, what string,
	check func([]*deploymentv1.Replica) error,
) []*deploymentv1.Replica {
	t.Helper()
	var replicas []*deploymentv1.Replica
	platform.Eventually(t, statusTimeout, what, func(ctx context.Context) error {
		current, err := statusOf(ctx, w, app)
		if err != nil {
			return err
		}
		replicas = current.region.GetReplicas()
		return check(replicas)
	})
	return replicas
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
