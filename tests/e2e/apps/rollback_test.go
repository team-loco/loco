package apps

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/locofile"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func TestFailedDeploymentRollsBack(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-rollback"
	host := w.Host(app)
	service := platform.ImageService(p.PublicImage, host)
	service.RollbackOnFailure = new(true)
	service.Health = nil
	service.Env = map[string]string{rollbackModeKey: "previous"}
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
	const poller = "e2e-rollback-poller"
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
	platform.Eventually(t, time.Minute, "requests before rollback", func(ctx context.Context) error {
		logs, logErr := p.Kubectl(ctx, "-n", p.Namespace, "logs", poller)
		if logErr != nil {
			return logErr
		}
		if strings.Count(logs, "ok") < 10 {
			return errors.New("waiting for ten successful requests")
		}
		return nil
	})
	shortenFailureDeadline(t, w, resourceID)
	crashing := withImage(file.Services[app], p.CrashingImage)
	crashing.Env = map[string]string{rollbackModeKey: "crashing"}
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
	if len(deployments) != 3 {
		t.Fatalf("deployments=%v, want the rollback, the failed deployment and the previous one", deployments)
	}
	restoredID, failed := deployments[0].GetId(), deployments[1]
	if failed.GetId() == previous || failed.GetStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED ||
		failed.GetPlacements()[0].GetLive() {
		t.Fatalf("failed deployment=%v", failed)
	}
	rollback := deployments[0]
	if rollback.GetTrigger() != deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_AUTO_ROLLBACK ||
		rollback.GetSourceDeploymentId() != previous {
		t.Fatalf("rollback deployment=%v, want an automatic rollback copying %s", rollback, previous)
	}
	platform.Eventually(t, statusTimeout, "the rollback to run", func(context.Context) error {
		restored := getDeployment(t, restoredID)
		if !restored.GetPlacements()[0].GetLive() ||
			restored.GetStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING {
			return fmt.Errorf("restored deployment=%v", restored)
		}
		if regionStatus(t, w, app).GetDeploymentId() != restoredID {
			return errors.New("resource does not run the rollback")
		}
		return nil
	})
	requireServingReplicas(t, getDeployment(t, restoredID), restoredID, 2)
	var workload appsv1.Deployment
	p.MustGet(t, &workload, "-n", w.Namespace, "deployment", locov1alpha1.WorkloadName(resourceID))
	if got := workloadEnv(workload, rollbackModeKey); got != "previous" {
		t.Fatalf("%s=%q after the rollback, want the previous deployment's %q", rollbackModeKey, got, "previous")
	}
	logs, err := p.Kubectl(t.Context(), "-n", p.Namespace, "logs", poller)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs, "fail") {
		t.Fatalf("requests failed during rollback:\n%s", logs)
	}
	if got := getDeployment(
		t,
		failed.GetId(),
	).GetStatus(); got != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED {
		t.Fatalf("failed deployment became %s", got)
	}
}

const rollbackModeKey = "ROLLBACK_MODE"

func workloadEnv(deployment appsv1.Deployment, name string) string {
	for _, variable := range deployment.Spec.Template.Spec.Containers[0].Env {
		if variable.Name == name {
			return variable.Value
		}
	}
	return ""
}

// 90s leaves the failing revision time to restart and take traffic before the rollout stalls.
func shortenFailureDeadline(t *testing.T, w *platform.Workspace, resourceID string) {
	t.Helper()
	p.MustKubectl(t, "-n", w.Namespace, "patch", "deployment", locov1alpha1.WorkloadName(resourceID),
		"--type=merge", "-p", `{"spec":{"progressDeadlineSeconds":90}}`)
}
