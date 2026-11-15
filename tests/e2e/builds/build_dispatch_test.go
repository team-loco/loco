package builds

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	agentBuildTimeout = time.Minute
	rolloutTimeout    = 4 * time.Minute
	retentionTimeout  = time.Minute
)

// TestBuildDispatch builds through the API and the agent: CreateBuild and its upload URL, the
// Build the agent creates, a deployment of the built image with a secret, a cached rebuild, a
// cancel, image retention and the purge of a deleted resource's images.
func TestBuildDispatch(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const name = "e2e-built-app"
	created, err := p.User.Resource.CreateResource(t.Context(), &resourcev1.CreateResourceRequest{
		WorkspaceId: w.ID,
		Name:        name,
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Domain: &domainv1.DomainInput{
			DomainSource:   domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED,
			Subdomain:      new(name),
			PlatformDomain: new(p.Domain),
		},
		Spec: &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: &resourcev1.ServiceSpec{
			Routing: &resourcev1.RoutingConfig{Port: new(int32(8080)), PathPrefix: "/"},
			Regions: map[string]*resourcev1.RegionTarget{
				p.Region: {Enabled: true, Cpu: "100m", Memory: "64Mi", MinReplicas: 1, MaxReplicas: 1},
			},
			HealthCheck: &deploymentv1.HealthCheckConfig{
				Path: "/healthz", IntervalSeconds: 5, TimeoutSeconds: 3, FailureThreshold: 3,
			},
		}}},
	})
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	resourceID := created.GetResourceId()
	d := &dispatch{resourceID: resourceID}

	t.Run("paths outside the source are rejected", d.rejectsPathsOutsideSource)

	first := startBuild(t, resourceID, "first")
	t.Run("the first build runs on the cluster", func(t *testing.T) {
		if !strings.HasPrefix(first.uploadURL, p.SourceBucketURL()+first.id+".tar.gz") {
			t.Errorf(
				"upload URL %s is not the path-style object %s%s.tar.gz",
				first.uploadURL,
				p.SourceBucketURL(),
				first.id,
			)
		}
		if first.startStatus != buildv1.BuildStatus_BUILD_STATUS_QUEUED {
			t.Errorf("StartBuild status %v, want queued", first.startStatus)
		}
		build := waitAgentBuild(t, first.id)
		if !strings.Contains(build.Spec.SourceURL, "X-Amz-Signature") {
			t.Errorf("the Build's source URL %s is not presigned", build.Spec.SourceURL)
		}
		finished := waitBuildFinished(t, first.id)
		if finished.GetStatus() != buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED {
			t.Fatalf("first build ended %v: %s", finished.GetStatus(), finished.GetMessage())
		}
		if !digestPattern.MatchString(finished.GetImageDigest()) {
			t.Errorf("image digest %q", finished.GetImageDigest())
		}
		if cache := cacheDigest(t, first.id); !digestPattern.MatchString(cache) {
			t.Errorf("cache digest %q", cache)
		}
		if finished.GetStartedAt() == nil || finished.GetFinishedAt() == nil {
			t.Error("the API recorded no start or finish time")
		}
		if p.SourceExists(t, first.id) {
			t.Error("the API kept the source after the build finished")
		}
		t.Logf("first build took %s", finished.GetFinishedAt().AsTime().Sub(finished.GetStartedAt().AsTime()))
	})

	t.Run("the built image deploys with a selected secret", func(t *testing.T) {
		const secret, value = "FIRST_DEPLOYMENT_SECRET", "e2e-first-deployment"
		if _, err := p.User.Secret.SetSecrets(t.Context(), &secretv1.SetSecretsRequest{
			ResourceId: resourceID, EnvironmentId: w.EnvironmentID, Values: map[string]string{secret: value},
		}); err != nil {
			t.Fatal(err)
		}
		deployAndWait(t, w, resourceID, first.id, secret)
		var deployment appsv1.Deployment
		p.MustGet(t, &deployment, "-n", w.Namespace, "deployment", "resource-"+resourceID)
		var mounted corev1.Secret
		p.MustGet(
			t,
			&mounted,
			"-n",
			w.Namespace,
			"secret",
			deployment.Spec.Template.Spec.Containers[0].EnvFrom[0].SecretRef.Name,
		)
		if got := string(mounted.Data[secret]); got != value {
			t.Errorf("the deployment's secret holds %q, want %q", got, value)
		}
		build := getBuild(t, first.id)
		want := build.GetImageRepository() + "@" + build.GetImageDigest()
		if image := deployment.Spec.Template.Spec.Containers[0].Image; image != want {
			t.Errorf("the app runs %s, want the build's image by digest", image)
		}
	})

	var second builtSource
	t.Run("a rebuild reuses the cache", func(t *testing.T) {
		second = startBuild(t, resourceID, "second")
		build := waitAgentBuild(t, second.id)
		want := getBuild(t, second.id).GetImageRepository() + "@" + cacheDigest(t, first.id)
		if build.Spec.CacheRef != want {
			t.Errorf("the second Build's cache ref %q, want the first build's cache %q", build.Spec.CacheRef, want)
		}
		finished := waitBuildFinished(t, second.id)
		if finished.GetStatus() != buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED {
			t.Fatalf("second build ended %v: %s", finished.GetStatus(), finished.GetMessage())
		}
		if logs := buildLogs(t, "build-"+second.id, "fetch"); !strings.Contains(logs, "cache restored") {
			t.Errorf("the fetch step did not restore the cache:\n%s", logs)
		}
		if logs := buildLogs(t, "build-"+second.id, buildStep); !strings.Contains(logs, "CACHED") {
			t.Errorf("the build step reused no cached layer:\n%s", logs)
		}
	})

	t.Run("a cancel reaches the cluster", func(t *testing.T) {
		third := startBuild(t, resourceID, "third")
		waitAgentBuild(t, third.id)
		if _, err := p.User.Build.CancelBuild(t.Context(), &buildv1.CancelBuildRequest{BuildId: third.id}); err != nil {
			t.Fatal(err)
		}
		if status := getBuild(t, third.id).GetStatus(); status != buildv1.BuildStatus_BUILD_STATUS_CANCELED {
			t.Errorf("canceled build status %v", status)
		}
		platform.Eventually(
			t,
			agentBuildTimeout,
			"the agent to delete the canceled Build",
			func(ctx context.Context) error {
				if found, err := p.Exists(
					ctx,
					"-n",
					p.BuildNamespace,
					"build",
					"build-"+third.id,
				); err != nil ||
					found {
					return fmt.Errorf("build-%s exists (%w)", third.id, err)
				}
				return nil
			},
		)
		if p.SourceExists(t, third.id) {
			t.Error("the API kept the canceled build's source")
		}
	})

	t.Run("retention deletes replaced images", func(t *testing.T) {
		firstBuild, secondBuild := getBuild(t, first.id), getBuild(t, second.id)
		path := strings.TrimPrefix(firstBuild.GetImageRepository(), p.RegistryHost+"/")
		if firstBuild.GetImageDigest() == secondBuild.GetImageDigest() {
			t.Fatal("the second build's source change produced the same image digest")
		}
		requireManifest(
			t,
			path,
			firstBuild.GetImageDigest(),
			200,
			"the deployed first build's image while a newer build exists",
		)
		if firstBuild.GetImageDeletedAt() != nil {
			t.Error("the first build is reported deleted while deployed")
		}
		deployAndWait(t, w, resourceID, second.id, "")
		platform.Eventually(
			t,
			retentionTimeout,
			"image retention to delete the replaced image",
			func(ctx context.Context) error {
				return manifestGone(ctx, path, firstBuild.GetImageDigest())
			},
		)
		requireManifest(t, path, cacheDigest(t, first.id), 404, "the replaced build's cache")
		requireManifest(t, path, secondBuild.GetImageDigest(), 200, "the deployed build's image")
		if getBuild(t, first.id).GetImageDeletedAt() == nil {
			t.Error("GetBuild does not report when the replaced build's image was deleted")
		}
		_, err := createDeployment(t, w, resourceID, first.id, "")
		if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
			t.Errorf("deploying the replaced build: code %v (%v), want failed_precondition", code, err)
		}
	})

	t.Run("a deleted resource's images are purged", func(t *testing.T) {
		build := getBuild(t, second.id)
		path := strings.TrimPrefix(build.GetImageRepository(), p.RegistryHost+"/")
		if _, err := p.User.Resource.DeleteResource(
			t.Context(),
			&resourcev1.DeleteResourceRequest{ResourceId: resourceID},
		); err != nil {
			t.Fatal(err)
		}
		platform.Eventually(
			t,
			retentionTimeout,
			"the deleted resource's images to be purged",
			func(ctx context.Context) error {
				return manifestGone(ctx, path, build.GetImageDigest())
			},
		)
	})
}

// buildAppDockerfile is the build-app fixture's Dockerfile.
const buildAppDockerfile = "deploy/Dockerfile"

// dispatch is the resource TestBuildDispatch builds.
type dispatch struct {
	resourceID string
}

type builtSource struct {
	id          string
	uploadURL   string
	startStatus buildv1.BuildStatus
}

// startBuild uploads the build-app fixture, changed so each label builds a new image, and starts
// a build of it.
func startBuild(t *testing.T, resourceID, label string) builtSource {
	t.Helper()
	source := t.TempDir()
	p.CopyFixture(t, "build-app", source)
	appendLogPrefix(t, source, label)
	archive := p.PackSource(t, source)
	info, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.User.Build.CreateBuild(t.Context(), &buildv1.CreateBuildRequest{
		ResourceId: resourceID, DockerfilePath: buildAppDockerfile, SourceSize: info.Size(),
	})
	if err != nil {
		t.Fatalf("create build %s: %v", label, err)
	}
	platform.Upload(t, created.GetUploadUrl(), archive)
	started, err := p.User.Build.StartBuild(t.Context(), &buildv1.StartBuildRequest{BuildId: created.GetBuildId()})
	if err != nil {
		t.Fatalf("start build %s: %v", label, err)
	}
	return builtSource{
		id:          created.GetBuildId(),
		uploadURL:   created.GetUploadUrl(),
		startStatus: started.GetBuild().GetStatus(),
	}
}

// appendLogPrefix changes the build-app fixture's source, so its image differs per label.
func appendLogPrefix(t *testing.T, dir, label string) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(dir, "main.go"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "\nfunc init() { log.SetPrefix(%q) }\n", label+" "); err != nil {
		t.Fatal(err)
	}
}

func getBuild(t *testing.T, id string) *buildv1.Build {
	t.Helper()
	response, err := p.User.Build.GetBuild(t.Context(), &buildv1.GetBuildRequest{BuildId: id})
	if err != nil {
		t.Fatal(err)
	}
	return response.GetBuild()
}

func cacheDigest(t *testing.T, id string) string {
	t.Helper()
	var digest string
	if err := p.DB.QueryRow(t.Context(), "SELECT cache_digest FROM builds WHERE id = $1", id).
		Scan(&digest); err != nil {
		t.Fatal(err)
	}
	return digest
}

func waitAgentBuild(t *testing.T, id string) *v1alpha1.Build {
	t.Helper()
	var build v1alpha1.Build
	platform.Eventually(t, agentBuildTimeout, "the agent to create Build build-"+id, func(ctx context.Context) error {
		return p.Get(ctx, &build, "-n", p.BuildNamespace, "build", "build-"+id)
	})
	return &build
}

func waitBuildFinished(t *testing.T, id string) *buildv1.Build {
	t.Helper()
	var build *buildv1.Build
	platform.Eventually(t, buildTimeout, "build "+id+" to finish", func(ctx context.Context) error {
		response, err := p.User.Build.GetBuild(ctx, &buildv1.GetBuildRequest{BuildId: id})
		if err != nil {
			return err
		}
		build = response.GetBuild()
		switch build.GetStatus() {
		case buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED,
			buildv1.BuildStatus_BUILD_STATUS_FAILED,
			buildv1.BuildStatus_BUILD_STATUS_CANCELED:
			return nil
		default:
			return fmt.Errorf("status %v", build.GetStatus())
		}
	})
	if build.GetStatus() != buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED {
		dumpBuild(t, "build-"+id)
	}
	return build
}

func createDeployment(
	t *testing.T,
	w *platform.Workspace,
	resourceID, buildID, secret string,
) (*deploymentv1.CreateDeploymentResponse, error) {
	t.Helper()
	request := &deploymentv1.CreateDeploymentRequest{
		ResourceId:    resourceID,
		Region:        p.Region,
		EnvironmentId: w.EnvironmentID,
		Spec: &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: &deploymentv1.ServiceDeploymentSpec{
				Build: &deploymentv1.BuildSource{Type: "dockerfile", BuildId: new(buildID)},
				Port:  new(int32(8080)),
			}},
		},
	}
	if secret != "" {
		request.SecretBindings = &deploymentv1.DeploymentSecretBindings{Names: []string{secret}}
	}
	return p.User.Deployment.CreateDeployment(t.Context(), request)
}

func deployAndWait(t *testing.T, w *platform.Workspace, resourceID, buildID, secret string) {
	t.Helper()
	created, err := createDeployment(t, w, resourceID, buildID, secret)
	if err != nil {
		t.Fatalf("deploy build %s: %v", buildID, err)
	}
	platform.Eventually(t, rolloutTimeout, "build "+buildID+" to roll out", func(ctx context.Context) error {
		response, err := p.User.Deployment.GetDeployment(
			ctx,
			&deploymentv1.GetDeploymentRequest{DeploymentId: created.GetDeploymentId()},
		)
		if err != nil {
			return err
		}
		if status := response.GetDeployment().
			GetStatus(); status != deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING {
			return fmt.Errorf("deployment %v", status)
		}
		return nil
	})
}

func requireManifest(t *testing.T, repository, digest string, want int, what string) {
	t.Helper()
	status, err := p.ManifestStatus(t.Context(), &platform.RegistryNodesUser, repository, digest)
	if err != nil {
		t.Fatal(err)
	}
	if status != want {
		t.Errorf("%s: manifest status %d, want %d", what, status, want)
	}
}

func manifestGone(ctx context.Context, repository, digest string) error {
	status, err := p.ManifestStatus(ctx, &platform.RegistryNodesUser, repository, digest)
	if err != nil {
		return err
	}
	if status != 404 {
		return fmt.Errorf("manifest status %d", status)
	}
	return nil
}

func (d *dispatch) rejectsPathsOutsideSource(t *testing.T) {
	resourceID := d.resourceID
	for _, path := range []struct{ dockerfile, context string }{
		{"../Dockerfile", "."}, {"/Dockerfile", "."}, {"Dockerfile", "../services"}, {"Dockerfile", "services//api"},
	} {
		build, err := p.User.Build.CreateBuild(t.Context(), &buildv1.CreateBuildRequest{
			ResourceId: resourceID, DockerfilePath: path.dockerfile, Context: path.context, SourceSize: 1,
		})
		if err == nil {
			if _, err = p.User.Build.CancelBuild(
				t.Context(),
				&buildv1.CancelBuildRequest{BuildId: build.GetBuildId()},
			); err != nil {
				t.Error(err)
			}
		}
		if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
			t.Errorf(
				"CreateBuild with dockerfile %q in context %q: code %v, want invalid_argument",
				path.dockerfile,
				path.context,
				code,
			)
		}
	}
}
