package apps

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/locofile"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

// TestATimedOutDeployKeepsThePreviousVersionServing deploys a crashing version over a serving one.
// Its deploy timeout stops it, and the previous version serves every request throughout.
func TestATimedOutDeployKeepsThePreviousVersionServing(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-deploy-timeout"
	host := w.Host(app)
	service := platform.ImageService(p.PublicImage, host)
	service.Health = nil
	service.Env = map[string]string{versionModeKey: "previous"}
	target := platform.OneReplica()
	target.Replicas = &locofile.Replicas{Min: new(int32(2)), Max: new(int32(2))}
	service.Regions = map[string]locofile.Region{p.Region: target}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	previous := regionStatus(t, w, app).GetDeploymentId()
	resourceID := w.ResourceID(t, app)
	gateway, err := p.GatewayService(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const poller = "e2e-deploy-timeout-poller"
	request := fmt.Sprintf(
		`wget -q -T 2 -O /dev/null --header "Host: %s" "http://%s.%s.svc.cluster.local/"`,
		host,
		gateway,
		p.Namespace,
	)
	p.StartPod(t, platform.ProbePod{Namespace: p.Namespace, Name: poller, Command: []string{
		"sh",
		"-c",
		fmt.Sprintf(
			`until %s; do sleep 1; done; while :; do if %s; then echo ok; else echo fail; fi; sleep 0.2; done`,
			request,
			request,
		),
	}})
	p.DeleteOnCleanup(t, "-n", p.Namespace, "pod", poller)
	platform.Eventually(t, time.Minute, "requests before the failed deploy", func(ctx context.Context) error {
		logs, logErr := p.Kubectl(ctx, "-n", p.Namespace, "logs", poller)
		if logErr != nil {
			return logErr
		}
		if strings.Count(logs, "ok") < 10 {
			return errors.New("waiting for ten successful requests")
		}
		return nil
	})
	crashing := withImage(file.Services[app], p.CrashingImage)
	crashing.Env = map[string]string{versionModeKey: "crashing"}
	crashing.Deploy = &locofile.Deploy{Timeout: new(int32(locofile.MinDeployTimeout))}
	file.Services[app] = crashing
	platform.WriteLocoFile(t, dir, file)
	result := w.Loco(t.Context(), dir, nil, "deploy", "--yes", "--wait")
	if result.Err == nil {
		t.Fatalf("crashing deploy succeeded: %s", result.Output())
	}
	response, err := p.User.Deployment.ListDeployments(
		t.Context(),
		&deploymentv1.ListDeploymentsRequest{ResourceId: resourceID},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployments := response.GetDeployments()
	if len(deployments) != 2 {
		t.Fatalf("deployments=%v, want the failed deployment and the previous one", deployments)
	}
	failed := deployments[0]
	if failed.GetId() == previous || failed.GetStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED {
		t.Fatalf("failed deployment=%v", failed)
	}
	platform.Eventually(
		t,
		statusTimeout,
		"the previous version to serve on every replica",
		func(context.Context) error {
			var sets appsv1.ReplicaSetList
			p.MustGet(t, &sets, "-n", w.Namespace, "replicasets", "-l", "app="+locov1alpha1.WorkloadName(resourceID))
			for _, set := range sets.Items {
				mode := templateEnv(set.Spec.Template.Spec, versionModeKey)
				if replicas := ptr.Deref(set.Spec.Replicas, 0); mode == "crashing" && replicas != 0 ||
					mode == "previous" && set.Status.ReadyReplicas != 2 {
					return fmt.Errorf("%s replica set %s: %d desired, %d ready", mode, set.Name, replicas,
						set.Status.ReadyReplicas)
				}
			}
			return nil
		},
	)
	requireServingReplicas(t, getDeployment(t, failed.GetId()), previous, 2)
	logs, err := p.Kubectl(t.Context(), "-n", p.Namespace, "logs", poller)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs, "fail") {
		t.Fatalf("requests failed during the failed deploy:\n%s", logs)
	}
}

const versionModeKey = "VERSION_MODE"

func templateEnv(pod corev1.PodSpec, name string) string {
	for _, variable := range pod.Containers[0].Env {
		if variable.Name == name {
			return variable.Value
		}
	}
	return ""
}
