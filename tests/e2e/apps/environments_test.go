package apps

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/locofile"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func TestEnvironmentsShareAClusterWithIndependentWorkloads(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app, preview = "environment-service", "preview"
	const markerKey = "ENVIRONMENT_MARKER"
	const secretKey = "IDENTITY_SECRET"
	const productionSecret = "production-fixture-secret"
	const previewSecret = "preview-fixture-secret"
	created, err := p.User.Environment.CreateEnvironment(
		t.Context(),
		&environmentv1.CreateEnvironmentRequest{WorkspaceId: w.ID, Name: preview},
	)
	if err != nil {
		t.Fatal(err)
	}
	previewID := created.GetEnvironmentId()
	previewNamespace := p.EnvironmentNamespace(previewID)
	service := platform.ImageService(p.PublicImage)
	service.Env = map[string]string{markerKey: platform.Production}
	service.Environments = map[string]locofile.Override{preview: {Env: map[string]string{markerKey: preview}}}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--env", platform.Production, "--yes", "--wait")
	for environment, value := range map[string]string{platform.Production: productionSecret, preview: previewSecret} {
		w.MustLoco(t, dir, "env", "set", "--service", app, "--env", environment, secretKey+"="+value)
	}
	service.Secrets = []string{secretKey}
	file.Services[app] = service
	platform.WriteLocoFile(t, dir, file)
	for _, environment := range []string{platform.Production, preview} {
		w.MustLoco(t, dir, "deploy", "--env", environment, "--yes", "--wait")
	}
	id := w.ResourceID(t, app)
	productionApp, err := p.Application(t.Context(), id, w.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	previewApp, err := p.Application(t.Context(), id, previewID)
	if err != nil {
		t.Fatal(err)
	}
	if productionApp.UID == previewApp.UID || productionApp.Name == previewApp.Name {
		t.Fatal("environments share an Application identity")
	}
	var placementEnvironments int
	if queryErr := p.DB.QueryRow(t.Context(), `SELECT count(DISTINCT environment_id) FROM placements
WHERE resource_id=$1 AND cluster_id=$2 AND NOT desired_deleted`, id, p.ClusterID).
		Scan(&placementEnvironments); queryErr != nil ||
		placementEnvironments != 2 {
		t.Fatalf("same-cluster placements=%d: %v", placementEnvironments, queryErr)
	}
	workload := loco.WorkloadName(id)
	deployment := func(namespace string) appsv1.Deployment {
		t.Helper()
		var result appsv1.Deployment
		p.MustGet(t, &result, "-n", namespace, "deployment", workload)
		return result
	}
	productionDeployment := deployment(w.Namespace)
	previewDeployment := deployment(previewNamespace)
	if productionDeployment.UID == previewDeployment.UID {
		t.Fatal("environments share a Deployment identity")
	}
	// Both namespaces remain in the same workspace network boundary.
	platform.Eventually(t, time.Minute, "cross-environment private routing", func(ctx context.Context) error {
		_, probeErr := p.Kubectl(
			ctx,
			"-n",
			w.Namespace,
			"exec",
			"deployment/"+workload,
			"--",
			"wget",
			"-q",
			"-T",
			"5",
			"-O",
			"/dev/null",
			"http://"+workload+"."+previewNamespace+".svc.cluster.local/",
		)
		return probeErr
	})
	w.MustLoco(t, dir, "resource", "scale", app, "--env", platform.Production, "--replicas", "2")
	override := service.Environments[preview]
	override.Env = map[string]string{markerKey: "preview-v2"}
	service.Environments[preview] = override
	file.Services[app] = service
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--env", preview, "--yes", "--wait")
	waitEnvironmentReplicas(t, productionDeployment, 2)
	checkRuntime := func(namespace, marker, environment, secret string) {
		t.Helper()
		out, runtimeErr := p.Kubectl(
			t.Context(),
			"-n",
			namespace,
			"exec",
			"deployment/"+workload,
			"--",
			"printenv",
			markerKey,
			"LOCO_ENVIRONMENT",
			secretKey,
		)
		if runtimeErr != nil || strings.TrimSpace(out) != marker+"\n"+environment+"\n"+secret {
			t.Fatalf("%s runtime values=%q: %v", namespace, out, runtimeErr)
		}
	}
	checkRuntime(w.Namespace, platform.Production, platform.Production, productionSecret)
	checkRuntime(previewNamespace, "preview-v2", preview, previewSecret)
	productionCopy := requireCopiedEnvironmentSecret(t, deployment(w.Namespace), secretKey, productionSecret)
	requireCopiedEnvironmentSecret(t, deployment(previewNamespace), secretKey, previewSecret)
	beforeDeletion := environmentPodRestarts(t, w.Namespace, workload)
	requireEnvironmentLineage(t, previewNamespace, workload, previewDeployment)
	override.Enabled = new(false)
	service.Environments[preview] = override
	file.Services[app] = service
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "infra", "apply", "--env", preview, "--yes", "--confirm-destructive")
	platform.Eventually(
		t,
		2*time.Minute,
		"preview Deployment/ReplicaSet/Pod garbage collection",
		func(ctx context.Context) error {
			if exists, lookupErr := p.Exists(ctx, "-n", p.Namespace, "application", previewApp.Name); lookupErr != nil {
				return lookupErr
			} else if exists {
				return fmt.Errorf("preview Application %s remains", previewApp.Name)
			}
			for _, kind := range []string{
				"deployments", "replicasets", corev1.ResourcePods.String(), corev1.ResourceSecrets.String(),
			} {
				exists, lookupErr := p.Exists(ctx, "-n", previewNamespace, kind, "-l", "app="+workload)
				if lookupErr != nil {
					return lookupErr
				}
				if exists {
					return fmt.Errorf("preview %s remain", kind)
				}
			}
			return nil
		},
	)
	if after := environmentPodRestarts(t, w.Namespace, workload); !maps.Equal(beforeDeletion, after) {
		t.Fatalf("preview deletion changed production pods/restarts: before=%v after=%v", beforeDeletion, after)
	}
	checkRuntime(w.Namespace, platform.Production, platform.Production, productionSecret)
	if after := requireCopiedEnvironmentSecret(
		t,
		deployment(w.Namespace),
		secretKey,
		productionSecret,
	); after.UID != productionCopy.UID ||
		!bytes.Equal(after.Data[secretKey], productionCopy.Data[secretKey]) {
		t.Fatal("preview deletion changed the production copied Secret")
	}
	remaining, err := p.Application(t.Context(), id, w.EnvironmentID)
	if err != nil || remaining.UID != productionApp.UID {
		t.Fatalf("production Application changed: %v %v", remaining, err)
	}
	if current := deployment(
		w.Namespace,
	); current.UID != productionDeployment.UID ||
		current.Status.ReadyReplicas != 2 {
		t.Fatalf("preview deletion changed production: %+v", current.Status)
	}
}

func requireEnvironmentLineage(t *testing.T, namespace, workload string, deployment appsv1.Deployment) {
	t.Helper()
	var previewSets appsv1.ReplicaSetList
	var previewPods corev1.PodList
	p.MustGet(t, &previewSets, "-n", namespace, "replicasets", "-l", "app="+workload)
	p.MustGet(t, &previewPods, "-n", namespace, corev1.ResourcePods.String(), "-l", "app="+workload)
	if len(previewSets.Items) == 0 || len(previewPods.Items) == 0 {
		t.Fatal("preview has no ReplicaSet/Pod lineage to collect")
	}
	ownedSets := make(map[string]bool, len(previewSets.Items))
	for _, set := range previewSets.Items {
		if len(set.OwnerReferences) != 1 || set.OwnerReferences[0].UID != deployment.UID {
			t.Fatalf("preview ReplicaSet %s owners=%v", set.UID, set.OwnerReferences)
		}
		ownedSets[string(set.UID)] = true
	}
	for _, pod := range previewPods.Items {
		if len(pod.OwnerReferences) != 1 || !ownedSets[string(pod.OwnerReferences[0].UID)] {
			t.Fatalf("preview Pod %s owners=%v", pod.UID, pod.OwnerReferences)
		}
	}
}

func waitEnvironmentReplicas(t *testing.T, deployment appsv1.Deployment, replicas int32) {
	t.Helper()
	platform.Eventually(t, time.Minute, "production replicas after preview redeploy", func(ctx context.Context) error {
		var current appsv1.Deployment
		if getErr := p.Get(ctx, &current, "-n", deployment.Namespace, "deployment", deployment.Name); getErr != nil {
			return getErr
		}
		if current.UID != deployment.UID || current.Status.ReadyReplicas != replicas ||
			current.Status.UpdatedReplicas != replicas {
			return fmt.Errorf("production UID=%s ready=%d", current.UID, current.Status.ReadyReplicas)
		}
		var pods corev1.PodList
		if getErr := p.Get(
			ctx,
			&pods,
			"-n",
			deployment.Namespace,
			corev1.ResourcePods.String(),
			"-l",
			"app="+deployment.Name,
		); getErr != nil {
			return getErr
		}
		if len(pods.Items) != int(replicas) {
			return fmt.Errorf("production has %d Pods while scaling settles", len(pods.Items))
		}
		for _, pod := range pods.Items {
			if !pod.DeletionTimestamp.IsZero() {
				return fmt.Errorf("production Pod %s is still retiring", pod.Name)
			}
		}
		return nil
	})
}

func environmentPodRestarts(t *testing.T, namespace, workload string) map[string]int32 {
	t.Helper()
	var pods corev1.PodList
	p.MustGet(t, &pods, "-n", namespace, corev1.ResourcePods.String(), "-l", "app="+workload)
	counts := make(map[string]int32, len(pods.Items))
	for _, pod := range pods.Items {
		counts[string(pod.UID)] = 0
		for _, container := range pod.Status.ContainerStatuses {
			counts[string(pod.UID)] += container.RestartCount
		}
	}
	return counts
}

func requireCopiedEnvironmentSecret(t *testing.T, deployment appsv1.Deployment, key, value string) corev1.Secret {
	t.Helper()
	sources := deployment.Spec.Template.Spec.Containers[0].EnvFrom
	if len(sources) != 1 || sources[0].SecretRef == nil {
		t.Fatalf("%s has no copied environment Secret reference", deployment.Namespace)
	}
	var secret corev1.Secret
	p.MustGet(t, &secret, "-n", deployment.Namespace, "secret", sources[0].SecretRef.Name)
	if string(secret.Data[key]) != value {
		t.Fatalf("%s copied Secret has the wrong fixture value", deployment.Namespace)
	}
	return secret
}
