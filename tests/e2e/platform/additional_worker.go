package platform

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
)

// AddWorker installs another real worker cluster against this platform's API.
// Its namespace, gateway, agent identity and Kubernetes API are independent.
func (p *Platform) AddWorker(t *testing.T, region *clusterv1.NewRegion) *Platform {
	t.Helper()
	worker := New(Features{Name: p.Name + "-second"})
	worker.runCtx = p.runCtx
	worker.DB = p.DB
	worker.Admin = p.Admin
	worker.User = p.User
	worker.APIURL = p.APIURL
	worker.binDir = p.binDir
	worker.PublicImage = p.PublicImage
	t.Cleanup(func() {
		worker.stopProcesses()
		if worker.keep {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), setupTimeout)
		defer cancel()
		if err := worker.lease.Release(cleanupCtx); err != nil {
			t.Errorf("release second worker: %v", err)
		}
	})
	for _, dir := range []string{worker.logDir, worker.adminHome} {
		if err := mkdirReadable(dir); err != nil {
			t.Fatal(err)
		}
	}
	ctx := t.Context()
	created, err := p.Admin.ClusterAdmin.CreateCluster(
		ctx,
		&clusterv1.CreateClusterRequest{RequestId: uuid.NewString(), Name: worker.run,
			Target: &clusterv1.CreateClusterRequest_NewRegion{NewRegion: region}},
	)
	if err != nil {
		t.Fatal(err)
	}
	worker.Region = created.GetRegion()
	worker.JoinToken = created.GetJoinToken().GetToken()
	if err = worker.setupKind(ctx); err != nil {
		t.Fatal(err)
	}
	worker.TargetPlatform = p.TargetPlatform
	if _, err = worker.Kubectl(
		ctx,
		"apply",
		"-f",
		filepath.Join(worker.root, "manifests", "namespaces", locoNamespace+".yaml"),
	); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.mise(
		ctx,
		[]string{"KIND_CLUSTER=" + worker.cluster},
		"crds:apply",
		"--without-builds",
	); err != nil {
		t.Fatal(err)
	}
	worker.RegistryHost = p.RegistryHost
	worker.ports.registry = p.ports.registry
	if err = worker.connectRegistry(ctx); err != nil {
		t.Fatal(err)
	}
	publicHost, _, ok := strings.Cut(p.PublicImage, "/")
	if !ok {
		t.Fatal("fixture image has no registry host")
	}
	if err = worker.registryHosts(ctx, publicHost, p.composeProject+"-public-image:5000"); err != nil {
		t.Fatal(err)
	}
	source := newImages(p).tag(controllerImage)
	target := newImages(worker).tag(controllerImage)
	if _, err = worker.Command(ctx, "docker", "tag", source, target); err != nil {
		t.Fatal(err)
	}
	images := newImages(worker)
	if err = images.load(ctx); err != nil {
		t.Fatal(err)
	}
	if err = worker.installWorker(ctx, images, false); err != nil {
		t.Fatal(err)
	}
	if err = worker.resetClusterState(ctx); err != nil {
		t.Fatal(err)
	}
	if err = worker.DeleteAgentKey(ctx); err != nil {
		t.Fatal(err)
	}
	key, err := worker.Kubectl(ctx, "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}")
	if err != nil {
		t.Fatal(err)
	}
	worker.ClusterKey = strings.TrimSpace(key)
	if err = worker.StartAgent(ctx, p.APIURL, worker.JoinToken); err != nil {
		t.Fatal(err)
	}
	if worker.ClusterID == p.ClusterID {
		t.Fatal("second worker reused first cluster identity")
	}
	if err = worker.forwardGateway(ctx); err != nil {
		t.Fatal(err)
	}
	return worker
}

// ForwardGateway exposes this worker's HTTPS listener through a host port-forward.
func (p *Platform) ForwardGateway(ctx context.Context) error {
	if err := p.forwardGateway(ctx); err != nil {
		return fmt.Errorf("forward worker gateway: %w", err)
	}
	return nil
}
